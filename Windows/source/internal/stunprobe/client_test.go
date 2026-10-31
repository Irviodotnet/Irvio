// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"context"
	"errors"
	"net/netip"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestClientDualStackBindingUsesMuxPort(t *testing.T) {
	_, mux, client := newTestStack(t, true, testRegistryConfig(), fastClientConfig())
	ipv4Server := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	ipv6Server := newTestSTUNServer(t, socketmux.IPv6, testServerBehavior{})

	type probeResult struct {
		result Result
		err    error
	}
	results := make(chan probeResult, 2)
	for _, server := range []netip.AddrPort{ipv4Server.addr, ipv6Server.addr} {
		go func(server netip.AddrPort) {
			result, err := client.Probe(context.Background(), server)
			results <- probeResult{result: result, err: err}
		}(server)
	}
	seen := map[socketmux.AddressFamily]Result{}
	for range 2 {
		probe := <-results
		if probe.err != nil {
			t.Fatal(probe.err)
		}
		seen[probe.result.Family] = probe.result
	}
	for family, result := range seen {
		if result.Server.Port() == 0 || result.Local.Port() != mux.Port() || result.Mapped.Port() != mux.Port() {
			t.Fatalf("%s result does not preserve mux port %d: %+v", family, mux.Port(), result)
		}
		// Two monotonic clock readings may coincide for a fast native loopback response.
		if result.Attempts != 1 || result.RoundTrip < 0 || result.CompletedAt.IsZero() {
			t.Fatalf("invalid %s result metadata: %+v", family, result)
		}
	}
	if request := nextRequest(t, ipv4Server); request.source.Port() != mux.Port() || !request.source.Addr().Is4() {
		t.Fatalf("IPv4 server observed %s, mux port %d", request.source, mux.Port())
	}
	if request := nextRequest(t, ipv6Server); request.source.Port() != mux.Port() || !request.source.Addr().Is6() {
		t.Fatalf("IPv6 server observed %s, mux port %d", request.source, mux.Port())
	}
	// Join the dispatcher before asserting its final counters.
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	stats := client.Stats()
	if stats.ProbesStarted != 2 || stats.ProbesSucceeded != 2 || stats.RequestsTransmitted != 2 || stats.ValidResponsesDispatched != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestClientRetransmitsSameTransaction(t *testing.T) {
	_, _, client := newTestStack(t, false, testRegistryConfig(), fastClientConfig())
	server := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{dropFirst: 1})
	result, err := client.Probe(context.Background(), server.addr)
	if err != nil {
		t.Fatal(err)
	}
	first := nextRequest(t, server)
	second := nextRequest(t, server)
	if first.id != second.id {
		t.Fatalf("retransmission changed transaction ID: %x != %x", first.id, second.id)
	}
	if result.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Attempts)
	}
	stats := client.Stats()
	if stats.RequestsTransmitted != 2 || stats.Retransmissions != 1 || stats.ProbesSucceeded != 1 {
		t.Fatalf("unexpected retransmission stats: %+v", stats)
	}
}

func TestClientTimeoutAndCancellation(t *testing.T) {
	_, _, client := newTestStack(t, false, testRegistryConfig(), Config{InitialRTO: 5 * time.Millisecond, MaxRTO: 10 * time.Millisecond, MaxAttempts: 2})
	timeoutServer := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{dropFirst: 100})
	if _, err := client.Probe(context.Background(), timeoutServer.addr); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout error = %v", err)
	}

	cancelServer := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{dropFirst: 100})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.Probe(ctx, cancelServer.addr)
		result <- err
	}()
	nextRequest(t, cancelServer)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	stats := client.Stats()
	if stats.ProbesTimedOut != 1 || stats.ProbesCanceled != 1 {
		t.Fatalf("unexpected timeout/cancel stats: %+v", stats)
	}
}

func TestClientRejectsMissingMappedAndReturnsBindingError(t *testing.T) {
	_, _, client := newTestStack(t, false, testRegistryConfig(), fastClientConfig())
	missing := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{missingMapped: true})
	if _, err := client.Probe(context.Background(), missing.addr); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("missing mapped error = %v", err)
	}
	errorServer := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{errorResponse: true})
	if _, err := client.Probe(context.Background(), errorServer.addr); err == nil {
		t.Fatal("Binding Error response was accepted")
	} else {
		var bindingErr *BindingError
		if !errors.As(err, &bindingErr) || bindingErr.Code != stun.CodeBadRequest {
			t.Fatalf("Binding Error = %v", err)
		}
	}
}

func TestResultValidationRejectsIdentityAndMappedEndpointMismatch(t *testing.T) {
	client := &Client{}
	server := netip.MustParseAddrPort("127.0.0.1:3478")
	local := netip.MustParseAddrPort("127.0.0.1:41000")
	id := transactionID{1, 3, 5, 7}
	tx := &transaction{id: id, policy: testResponsePolicy(server)}
	request := BindingRequest{Destination: server, ExpectedSuccessSource: server, AllowedErrorSources: []netip.AddrPort{server}}
	valid, err := buildResponse(id, local, false, false)
	if err != nil {
		t.Fatal(err)
	}
	validEvent := responseEvent{message: valid, source: server, family: socketmux.IPv4}
	if result, err := client.resultFromResponse(validEvent, tx, request, local, socketmux.IPv4, time.Now(), 1); err != nil || result.MappedEndpoint != local {
		t.Fatalf("valid response result=%+v error=%v", result, err)
	}

	wrongID, err := buildResponse(transactionID{9}, local, false, false)
	if err != nil {
		t.Fatal(err)
	}
	ipv6Mapped, err := buildResponse(id, netip.MustParseAddrPort("[::1]:41000"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	zeroPort, err := buildResponse(id, netip.MustParseAddrPort("127.0.0.1:0"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	unspecified, err := buildResponse(id, netip.MustParseAddrPort("0.0.0.0:41000"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]responseEvent{
		"nil message":        {source: server, family: socketmux.IPv4},
		"transaction ID":     {message: wrongID, source: server, family: socketmux.IPv4},
		"source":             {message: valid, source: netip.MustParseAddrPort("127.0.0.1:3479"), family: socketmux.IPv4},
		"family metadata":    {message: valid, source: server, family: socketmux.IPv6},
		"mapped family":      {message: ipv6Mapped, source: server, family: socketmux.IPv4},
		"mapped zero port":   {message: zeroPort, source: server, family: socketmux.IPv4},
		"mapped unspecified": {message: unspecified, source: server, family: socketmux.IPv4},
	}
	for name, event := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := client.resultFromResponse(event, tx, request, local, socketmux.IPv4, time.Now(), 1); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestClientConfigurationAndServerValidation(t *testing.T) {
	registry, err := NewRegistry(testRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(nil, registry, fastClientConfig()); err == nil {
		t.Fatal("nil endpoint accepted")
	}
	config := socketmux.DefaultConfig()
	config.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	config.IPv6Policy = socketmux.FamilyDisabled
	mux, err := socketmux.New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	if _, err := NewClient(mux.Control(), nil, fastClientConfig()); err == nil {
		t.Fatal("nil registry accepted")
	}
	invalidConfigs := []Config{{}, {InitialRTO: -1, MaxRTO: time.Second, MaxAttempts: 1}, {InitialRTO: time.Second, MaxRTO: time.Millisecond, MaxAttempts: 1}, {InitialRTO: time.Millisecond, MaxRTO: time.Second, MaxAttempts: 33}}
	for _, invalid := range invalidConfigs {
		if _, err := NewClient(mux.Control(), registry, invalid); err == nil {
			t.Fatalf("invalid config accepted: %+v", invalid)
		}
	}
	client, err := NewClient(mux.Control(), registry, fastClientConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := NewClient(mux.Control(), registry, fastClientConfig()); err == nil {
		t.Fatal("second dispatcher attached to registry")
	}
	invalidServers := []netip.AddrPort{{}, netip.MustParseAddrPort("0.0.0.0:3478"), netip.MustParseAddrPort("127.0.0.1:0"), netip.MustParseAddrPort("[::ffff:127.0.0.1]:3478")}
	for _, server := range invalidServers {
		if _, err := client.Probe(context.Background(), server); !errors.Is(err, ErrInvalidServer) {
			t.Fatalf("server %s error = %v", server, err)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Probe(canceled, netip.MustParseAddrPort("127.0.0.1:3478")); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled probe error = %v", err)
	}
	if _, err := client.Probe(nil, netip.MustParseAddrPort("127.0.0.1:3478")); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestWrongSourceCannotCompleteProbe(t *testing.T) {
	_, mux, client := newTestStack(t, false, testRegistryConfig(), Config{InitialRTO: time.Second, MaxRTO: time.Second, MaxAttempts: 1})
	gate := make(chan struct{})
	server := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{gate: gate})
	probeDone := make(chan error, 1)
	go func() {
		_, err := client.Probe(context.Background(), server.addr)
		probeDone <- err
	}()
	request := nextRequest(t, server)
	intruder := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	response, err := buildResponse(request.id, request.source, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := intruder.conn.WriteToUDPAddrPort(response.Raw, request.source); err != nil {
		t.Fatal(err)
	}
	if datagram := readData(t, mux); datagram.Source != intruder.addr {
		t.Fatalf("wrong-source packet came from %s, want %s", datagram.Source, intruder.addr)
	}
	select {
	case err := <-probeDone:
		t.Fatalf("wrong source completed probe: %v", err)
	default:
	}
	close(gate)
	if err := <-probeDone; err != nil {
		t.Fatal(err)
	}
	if client.Stats().WrongSourceResponsesIgnored == 0 {
		t.Fatal("wrong-source response was not counted")
	}
}

func TestWrongTransactionAndMalformedResponsesRemainData(t *testing.T) {
	_, mux, client := newTestStack(t, false, testRegistryConfig(), Config{InitialRTO: 5 * time.Millisecond, MaxRTO: 5 * time.Millisecond, MaxAttempts: 1})
	wrongID := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{wrongTransaction: true})
	if _, err := client.Probe(context.Background(), wrongID.addr); !errors.Is(err, ErrTimeout) {
		t.Fatalf("wrong transaction error = %v", err)
	}
	if datagram := readData(t, mux); datagram.Source != wrongID.addr {
		t.Fatalf("wrong-ID packet source = %s", datagram.Source)
	}

	malformed := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{malformed: true})
	if _, err := client.Probe(context.Background(), malformed.addr); !errors.Is(err, ErrTimeout) {
		t.Fatalf("malformed response error = %v", err)
	}
	if datagram := readData(t, mux); datagram.Source != malformed.addr {
		t.Fatalf("malformed packet source = %s", datagram.Source)
	}
	stats := client.Stats()
	if stats.UnknownTransactionResponsesIgnored == 0 || stats.MalformedSTUNLikePackets == 0 {
		t.Fatalf("unexpected security stats: %+v", stats)
	}
}

func TestUnknownValidAndSTUNLookingMalformedRemainData(t *testing.T) {
	_, mux, client := newTestStack(t, false, testRegistryConfig(), fastClientConfig())
	sender := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	destination, _ := mux.LocalAddr(socketmux.IPv4)
	unknown, err := buildResponse(transactionID{7, 7, 7}, destination, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.conn.WriteToUDPAddrPort(unknown.Raw, destination); err != nil {
		t.Fatal(err)
	}
	if datagram := readData(t, mux); string(datagram.Payload) != string(unknown.Raw) {
		t.Fatal("unknown valid STUN packet was not delivered intact to Data")
	}
	malformed := malformedAttributeResponse(transactionID{8, 8, 8})
	if _, err := sender.conn.WriteToUDPAddrPort(malformed, destination); err != nil {
		t.Fatal(err)
	}
	if datagram := readData(t, mux); string(datagram.Payload) != string(malformed) {
		t.Fatal("malformed STUN-like packet was not delivered intact to Data")
	}
	stats := client.Stats()
	if stats.UnknownTransactionResponsesIgnored != 1 || stats.MalformedSTUNLikePackets != 1 {
		t.Fatalf("unexpected unknown/malformed stats: %+v", stats)
	}
}

func TestLateResponseIsQuarantined(t *testing.T) {
	_, mux, client := newTestStack(t, false, testRegistryConfig(), fastClientConfig())
	server := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	if _, err := client.Probe(context.Background(), server.addr); err != nil {
		t.Fatal(err)
	}
	request := nextRequest(t, server)
	before := client.Stats().RetiredLateResponsesDropped
	if err := server.sendSuccess(request); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return client.Stats().RetiredLateResponsesDropped > before })
	assertNoData(t, mux)
}

func TestDuplicateResponseCannotCompleteTwice(t *testing.T) {
	_, mux, client := newTestStack(t, false, testRegistryConfig(), fastClientConfig())
	server := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{duplicate: true})
	if _, err := client.Probe(context.Background(), server.addr); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return client.Stats().RetiredLateResponsesDropped > 0 })
	stats := client.Stats()
	if stats.ProbesSucceeded != 1 || stats.ValidResponsesDispatched != 1 {
		t.Fatalf("duplicate completed transaction more than once: %+v", stats)
	}
	assertNoData(t, mux)
}

func TestOversizedPacketIsDroppedBeforeClassifier(t *testing.T) {
	registry, err := NewRegistry(testRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	config := socketmux.DefaultConfig()
	config.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	config.IPv6Policy = socketmux.FamilyDisabled
	config.MaxDatagramSize = 64
	config.Classifier = NewClassifier(registry)
	mux, err := socketmux.New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	sender := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	destination, _ := mux.LocalAddr(socketmux.IPv4)
	payload := make([]byte, 128)
	copy(payload[4:8], []byte{0x21, 0x12, 0xa4, 0x42})
	if _, err := sender.conn.WriteToUDPAddrPort(payload, destination); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return mux.Stats().OversizedDrops == 1 })
	if registry.stats.malformedSTUNLike.Load() != 0 {
		t.Fatal("oversized packet reached STUN classifier")
	}
	assertNoData(t, mux)
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testDeadline)
	defer cancel()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal("condition not satisfied before deadline")
		default:
		}
		runtime.Gosched()
	}
}

func TestClientStatsConcurrent(t *testing.T) {
	_, _, client := newTestStack(t, false, testRegistryConfig(), fastClientConfig())
	server := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = client.Probe(context.Background(), server.addr)
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				_ = client.Stats()
			}
		}()
	}
	wg.Wait()
	if stats := client.Stats(); stats.ProbesSucceeded != 16 {
		t.Fatalf("successful probes = %d", stats.ProbesSucceeded)
	}
}
