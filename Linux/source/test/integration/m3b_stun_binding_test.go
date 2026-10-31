// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package integration_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

type m3bServerBehavior struct {
	dropFirst int
	gate      <-chan struct{}
}

type m3bRequest struct {
	source netip.AddrPort
	id     [stun.TransactionIDSize]byte
}

type m3bServer struct {
	conn      *net.UDPConn
	addr      netip.AddrPort
	behavior  m3bServerBehavior
	requests  chan m3bRequest
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func TestM3BTransactionAwareDualStackBinding(t *testing.T) {
	requireM3BIPv6(t)
	registryConfig := stunprobe.DefaultRegistryConfig()
	registryConfig.RetiredGrace = time.Second
	registry, err := stunprobe.NewRegistry(registryConfig)
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyRequired
	muxConfig.IPv6Bind = netip.IPv6Loopback()
	muxConfig.Classifier = stunprobe.NewClassifier(registry)
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	clientConfig := stunprobe.Config{InitialRTO: 20 * time.Millisecond, MaxRTO: 40 * time.Millisecond, MaxAttempts: 3}
	client, err := stunprobe.NewClient(mux.Control(), registry, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ipv4 := newM3BServer(t, "udp4", netip.MustParseAddr("127.0.0.1"), m3bServerBehavior{})
	ipv6 := newM3BServer(t, "udp6", netip.IPv6Loopback(), m3bServerBehavior{})
	type probeResult struct {
		result stunprobe.Result
		err    error
	}
	probeResults := make(chan probeResult, 2)
	for _, server := range []netip.AddrPort{ipv4.addr, ipv6.addr} {
		go func(server netip.AddrPort) {
			result, probeErr := client.Probe(context.Background(), server)
			probeResults <- probeResult{result: result, err: probeErr}
		}(server)
	}
	for range 2 {
		probe := <-probeResults
		if probe.err != nil {
			t.Fatal(probe.err)
		}
		if probe.result.Local.Port() != mux.Port() || probe.result.Mapped.Port() != mux.Port() {
			t.Fatalf("Binding result does not use mux port %d: %+v", mux.Port(), probe.result)
		}
	}
	for _, server := range []*m3bServer{ipv4, ipv6} {
		request := nextM3BRequest(t, server)
		if request.source.Port() != mux.Port() {
			t.Fatalf("server %s observed source %s, mux port %d", server.addr, request.source, mux.Port())
		}
	}

	verifyM3BDataWrites(t, mux, socketmux.IPv4)
	verifyM3BDataWrites(t, mux, socketmux.IPv6)

	firstGate := make(chan struct{})
	secondGate := make(chan struct{})
	first := newM3BServer(t, "udp4", netip.MustParseAddr("127.0.0.1"), m3bServerBehavior{gate: firstGate})
	second := newM3BServer(t, "udp4", netip.MustParseAddr("127.0.0.1"), m3bServerBehavior{gate: secondGate})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { _, probeErr := client.Probe(context.Background(), first.addr); firstDone <- probeErr }()
	go func() { _, probeErr := client.Probe(context.Background(), second.addr); secondDone <- probeErr }()
	firstRequest := nextM3BRequest(t, first)
	nextM3BRequest(t, second)
	close(secondGate)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-firstDone:
		t.Fatalf("out-of-order isolation failed: %v", err)
	default:
	}
	close(firstGate)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}

	retry := newM3BServer(t, "udp4", netip.MustParseAddr("127.0.0.1"), m3bServerBehavior{dropFirst: 1})
	result, err := client.Probe(context.Background(), retry.addr)
	if err != nil || result.Attempts != 2 {
		t.Fatalf("retransmitted probe result=%+v error=%v", result, err)
	}

	wrongGate := make(chan struct{})
	expected := newM3BServer(t, "udp4", netip.MustParseAddr("127.0.0.1"), m3bServerBehavior{gate: wrongGate})
	wrongDone := make(chan error, 1)
	go func() { _, probeErr := client.Probe(context.Background(), expected.addr); wrongDone <- probeErr }()
	expectedRequest := nextM3BRequest(t, expected)
	intruder, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer intruder.Close()
	wrongResponse := mustM3BResponse(t, expectedRequest.id, expectedRequest.source)
	if _, err := intruder.WriteToUDPAddrPort(wrongResponse, expectedRequest.source); err != nil {
		t.Fatal(err)
	}
	wrongDatagram := readM3BData(t, mux)
	if wrongDatagram.Source != intruder.LocalAddr().(*net.UDPAddr).AddrPort() {
		t.Fatalf("wrong-source response was not preserved as Data: %s", wrongDatagram.Source)
	}
	select {
	case err := <-wrongDone:
		t.Fatalf("wrong-source response completed transaction: %v", err)
	default:
	}
	close(wrongGate)
	if err := <-wrongDone; err != nil {
		t.Fatal(err)
	}

	unknownID := firstRequest.id
	unknownID[0] ^= 0xff
	unknown := mustM3BResponse(t, unknownID, expectedRequest.source)
	if _, err := intruder.WriteToUDPAddrPort(unknown, expectedRequest.source); err != nil {
		t.Fatal(err)
	}
	if datagram := readM3BData(t, mux); string(datagram.Payload) != string(unknown) {
		t.Fatal("unknown valid STUN response did not remain Data")
	}

	beforeLate := client.Stats().RetiredLateResponsesDropped
	late := mustM3BResponse(t, firstRequest.id, firstRequest.source)
	if _, err := first.conn.WriteToUDPAddrPort(late, firstRequest.source); err != nil {
		t.Fatal(err)
	}
	eventuallyM3B(t, func() bool { return client.Stats().RetiredLateResponsesDropped > beforeLate })
	assertNoM3BData(t, mux)

	cancelServer := newM3BServer(t, "udp4", netip.MustParseAddr("127.0.0.1"), m3bServerBehavior{dropFirst: 100})
	successServer := newM3BServer(t, "udp4", netip.MustParseAddr("127.0.0.1"), m3bServerBehavior{})
	cancelContext, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { _, probeErr := client.Probe(cancelContext, cancelServer.addr); canceled <- probeErr }()
	nextM3BRequest(t, cancelServer)
	cancel()
	if _, err := client.Probe(context.Background(), successServer.addr); err != nil {
		t.Fatal(err)
	}
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled concurrent probe error = %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	verifyM3BDataInbound(t, mux)

	secondClient, err := stunprobe.NewClient(mux.Control(), registry, clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	blocking := newM3BServer(t, "udp4", netip.MustParseAddr("127.0.0.1"), m3bServerBehavior{dropFirst: 100})
	blockedProbe := make(chan error, 1)
	go func() {
		_, probeErr := secondClient.Probe(context.Background(), blocking.addr)
		blockedProbe <- probeErr
	}()
	nextM3BRequest(t, blocking)
	if err := mux.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-blockedProbe; !errors.Is(err, stunprobe.ErrTransportClosed) {
		t.Fatalf("probe after mux close error = %v", err)
	}
	if err := secondClient.Close(); err != nil {
		t.Fatal(err)
	}
}

func newM3BServer(t *testing.T, network string, bind netip.Addr, behavior m3bServerBehavior) *m3bServer {
	t.Helper()
	conn, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(bind, 0)))
	if err != nil {
		t.Fatal(err)
	}
	server := &m3bServer{
		conn: conn, addr: conn.LocalAddr().(*net.UDPAddr).AddrPort(), behavior: behavior,
		requests: make(chan m3bRequest, 128), done: make(chan struct{}),
	}
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		buffer := make([]byte, socketmux.MaxUDPPayloadSize)
		requestCount := 0
		for {
			n, source, readErr := conn.ReadFromUDPAddrPort(buffer)
			if readErr != nil {
				if !errors.Is(readErr, net.ErrClosed) {
					t.Errorf("integration STUN read: %v", readErr)
				}
				return
			}
			request := stun.NewWithOptions(stun.WithStrict(true))
			if request.UnmarshalBinary(buffer[:n]) != nil || request.Type != stun.BindingRequest {
				continue
			}
			requestCount++
			observed := m3bRequest{source: source, id: request.TransactionID}
			select {
			case server.requests <- observed:
			case <-server.done:
				return
			}
			if requestCount <= behavior.dropFirst {
				continue
			}
			if behavior.gate != nil {
				select {
				case <-behavior.gate:
				case <-server.done:
					return
				}
			}
			response, responseErr := m3bResponse(observed.id, source)
			if responseErr != nil {
				t.Errorf("build integration STUN response: %v", responseErr)
				return
			}
			if _, writeErr := conn.WriteToUDPAddrPort(response, source); writeErr != nil && !errors.Is(writeErr, net.ErrClosed) {
				t.Errorf("integration STUN write: %v", writeErr)
				return
			}
		}
	}()
	t.Cleanup(func() {
		server.closeOnce.Do(func() {
			close(server.done)
			_ = server.conn.Close()
			server.wg.Wait()
		})
	})
	return server
}

func m3bResponse(id [stun.TransactionIDSize]byte, mapped netip.AddrPort) ([]byte, error) {
	message := stun.MustBuild(stun.BindingSuccess)
	message.TransactionID = id
	message.WriteTransactionID()
	attribute := stun.XORMappedAddress{IP: net.IP(mapped.Addr().AsSlice()), Port: int(mapped.Port())}
	if err := attribute.AddTo(message); err != nil {
		return nil, err
	}
	return append([]byte(nil), message.Raw...), nil
}

func mustM3BResponse(t *testing.T, id [stun.TransactionIDSize]byte, mapped netip.AddrPort) []byte {
	t.Helper()
	response, err := m3bResponse(id, mapped)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func nextM3BRequest(t *testing.T, server *m3bServer) m3bRequest {
	t.Helper()
	select {
	case request := <-server.requests:
		return request
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for integration STUN request")
		return m3bRequest{}
	}
}

func verifyM3BDataWrites(t *testing.T, mux *socketmux.Mux, family socketmux.AddressFamily) {
	t.Helper()
	network := "udp4"
	bind := netip.MustParseAddr("127.0.0.1")
	if family == socketmux.IPv6 {
		network = "udp6"
		bind = netip.IPv6Loopback()
	}
	receiver, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(bind, 0)))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	destination := receiver.LocalAddr().(*net.UDPAddr).AddrPort()
	if _, err := mux.Data().WriteTo(context.Background(), []byte("future-data"), destination); err != nil {
		t.Fatal(err)
	}
	if err := receiver.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 32)
	_, source, err := receiver.ReadFromUDPAddrPort(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if source.Port() != mux.Port() {
		t.Fatalf("%s Data source %s does not use mux port %d", family, source, mux.Port())
	}
}

func verifyM3BDataInbound(t *testing.T, mux *socketmux.Mux) {
	t.Helper()
	sender, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	destination, _ := mux.LocalAddr(socketmux.IPv4)
	if _, err := sender.WriteToUDPAddrPort([]byte("data-after-client-close"), destination); err != nil {
		t.Fatal(err)
	}
	if datagram := readM3BData(t, mux); string(datagram.Payload) != "data-after-client-close" {
		t.Fatalf("Data after client close = %q", datagram.Payload)
	}
}

func readM3BData(t *testing.T, mux *socketmux.Mux) socketmux.Datagram {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	datagram, err := mux.Data().Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return datagram
}

func assertNoM3BData(t *testing.T, mux *socketmux.Mux) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if datagram, err := mux.Data().Read(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected late Data datagram %+v, error %v", datagram, err)
	}
}

func eventuallyM3B(t *testing.T, condition func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal("integration condition not satisfied")
		default:
		}
		runtime.Gosched()
	}
}

func requireM3BIPv6(t *testing.T) {
	t.Helper()
	conn, err := net.ListenUDP("udp6", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv6Loopback(), 0)))
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	_ = conn.Close()
}
