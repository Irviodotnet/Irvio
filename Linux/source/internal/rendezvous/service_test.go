// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/sessionestablish"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type echoSignalHandler struct {
	calls atomic.Uint64
	delay atomic.Int64
	seen  chan []byte
}

type countryServerFixture map[string][]peer.ID

func (f countryServerFixture) Servers(_ context.Context, country string, limit int) ([]peer.ID, error) {
	values := append([]peer.ID(nil), f[country]...)
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func (h *echoSignalHandler) HandleSignal(ctx context.Context, _ peer.ID, signed []byte) ([]byte, error) {
	h.calls.Add(1)
	if delay := time.Duration(h.delay.Load()); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	select {
	case h.seen <- append([]byte(nil), signed...):
	default:
	}
	return append([]byte("response:"), signed...), nil
}

type brokerOfferProcessor struct {
	exit peer.ID
	now  time.Time
}

func (p brokerOfferProcessor) ProcessOffer(_ context.Context, end sessionestablish.EndPeerContext, offer *irviov1.SessionOffer) (sessionestablish.AnswerResult, error) {
	return sessionestablish.AnswerResult{
		ICEAnswer: &irviov1.IceAnswer{
			ProtocolVersion: 1, SessionId: append([]byte(nil), offer.IceOffer.SessionId...),
			InitiatorPeerId: []byte(end.RemotePeer), ResponderPeerId: []byte(p.exit),
			Purpose:       irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT,
			Role:          irviov1.IceRole_ICE_ROLE_CONTROLLED,
			Description:   testICEDescription([]byte{10, 0, 0, 2}, 50002, "exit"),
			CreatedAtUnix: p.now.Unix(), ExpiresAtUnix: p.now.Add(time.Minute).Unix(),
		},
		ProvisionalGrant: &irviov1.TunnelLease{
			LeaseId: bytes.Repeat([]byte{5}, 16), ExitPeerId: []byte(p.exit),
			ClientPeerId: []byte(end.RemotePeer), Ipv4Address: []byte{10, 88, 0, 2},
			Ipv4PrefixBits: 32, ValidUntilUnix: p.now.Add(5 * time.Minute).Unix(),
			Generation: 1, SessionToken: append([]byte(nil), end.OwnerToken[:16]...),
		},
		ExitControlAddresses: [][]byte{{10, 88, 0, 1}}, ExitControlPort: 41001,
		AWGProfileHash: [32]byte{1}, ActivationDeadline: p.now.Add(10 * time.Second),
	}, nil
}

func testICEDescription(address []byte, port uint32, foundation string) *irviov1.IceDescription {
	return &irviov1.IceDescription{
		Credentials: &irviov1.IceCredentials{Ufrag: foundation + "Ufrag", Pwd: "1234567890123456789012"},
		Candidates: []*irviov1.IceCandidate{{
			Type:    irviov1.IceCandidateType_ICE_CANDIDATE_TYPE_HOST,
			Network: irviov1.IceNetworkType_ICE_NETWORK_TYPE_UDP4,
			Address: address, Port: port, Foundation: foundation, Priority: 100, Component: 1,
		}},
	}
}

func newRendezvousHost(t *testing.T) (host.Host, crypto.PrivKey) {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := libp2p.New(libp2p.Identity(key), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { value.Close() })
	return value, key
}

func connectRendezvousHosts(t *testing.T, from, to host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := from.Connect(ctx, peer.AddrInfo{ID: to.ID(), Addrs: to.Addrs()}); err != nil {
		t.Fatal(err)
	}
}

func hostControlAddress(t *testing.T, value host.Host) string {
	t.Helper()
	addresses, err := peer.AddrInfoToP2pAddrs(&peer.AddrInfo{ID: value.ID(), Addrs: value.Addrs()})
	if err != nil || len(addresses) == 0 {
		t.Fatalf("control address: %v", err)
	}
	return addresses[0].String()
}

func TestServiceRegistrationSamplingAndLocalBroker(t *testing.T) {
	serverHost, serverKey := newRendezvousHost(t)
	exitHost, exitKey := newRendezvousHost(t)
	clientHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, exitHost, serverHost)
	connectRendezvousHosts(t, clientHost, serverHost)

	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = 100 * time.Millisecond
	config.RegistrationStale = time.Second
	config.RegistrationExpiry = 2 * time.Second
	registry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: 11,
		Signer: serverKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, serverHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })

	handler := &echoSignalHandler{seen: make(chan []byte, 1)}
	registration, err := OpenRegistration(context.Background(), RegistrationClientOptions{
		Host: exitHost, Server: serverHost.ID(),
		SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, 1, true),
		Config:           config, Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = registration.Close(ctx)
	})

	client, err := NewClient(clientHost, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	candidates, partial, err := client.GetCandidates(ctx, serverHost.ID(), "DE", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if partial || len(candidates) != 1 || candidates[0].Descriptor.Subject != exitHost.ID() {
		t.Fatalf("candidate response: count=%d partial=%t", len(candidates), partial)
	}
	request, _ := NewRequestID(nil)
	route := candidates[0].Routes[0]
	response, err := client.Signal(ctx, serverHost.ID(), &irviov1.RendezvousSignal{
		ProtocolVersion: ProtocolVersion, RequestHandle: append([]byte(nil), request[:]...),
		TargetPeerId: []byte(exitHost.ID()),
		OwnerRoute: &irviov1.OwnerRoute{
			ServerPeerId: []byte(route.ServerPeer), ServerIncarnation: route.ServerIncarnation,
			RegistrationId:   append([]byte(nil), route.RegistrationID[:]...),
			ControlAddresses: append([]string(nil), route.ControlAddresses...),
		},
		SignedMessage: []byte("prepare-transcript"), DeadlineUnixMilli: time.Now().Add(2 * time.Second).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(response.SignedMessage) != "response:prepare-transcript" || handler.calls.Load() != 1 {
		t.Fatalf("broker response=%q calls=%d", response.SignedMessage, handler.calls.Load())
	}
	select {
	case seen := <-handler.seen:
		if string(seen) != "prepare-transcript" {
			t.Fatalf("handler payload=%q", seen)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestRegistrationUpdateWaitsForAcknowledgedDescriptor(t *testing.T) {
	serverHost, serverKey := newRendezvousHost(t)
	exitHost, exitKey := newRendezvousHost(t)
	connectRendezvousHosts(t, exitHost, serverHost)
	base := time.Unix(1_800_400_000, 0).UTC()
	var clock atomic.Int64
	clock.Store(base.UnixNano())
	config := DefaultConfig()
	config.Now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	config.ObservedIPPolicy.AllowLoopback = true
	config.Admission.RegisterBurst = 1
	config.HeartbeatInterval = time.Minute
	config.RegistrationStale = 2 * time.Minute
	config.RegistrationExpiry = 3 * time.Minute
	registry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: 13,
		Signer: serverKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, serverHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	registration, err := OpenRegistration(context.Background(), RegistrationClientOptions{
		Host: exitHost, Server: serverHost.ID(),
		SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), base, 1, 1, true),
		Config:           config, Handler: &echoSignalHandler{seen: make(chan []byte, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = registration.Close(ctx)
	})
	signed := testSignedExit(t, exitKey, exitHost.ID(), base, 1, 2, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := registration.Update(ctx, signed); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("first update error=%v, want rate limit", err)
	}
	clock.Add(config.Admission.RegisterInterval.Nanoseconds())
	if err := registration.Update(ctx, signed); err != nil {
		t.Fatal(err)
	}
	snapshot := registration.Snapshot()
	if snapshot.ReceiptSequence < 2 {
		t.Fatalf("acknowledged receipt sequence=%d", snapshot.ReceiptSequence)
	}
	candidates, err := registry.Sample("DE", 1, nil)
	if err != nil || len(candidates) != 1 || candidates[0].Descriptor.Version.Sequence != 2 {
		t.Fatalf("updated registry candidates=%d err=%v", len(candidates), err)
	}
}

func TestRegistrationCloseAfterOwnerLossIsSuccessfulCleanup(t *testing.T) {
	serverHost, serverKey := newRendezvousHost(t)
	exitHost, exitKey := newRendezvousHost(t)
	connectRendezvousHosts(t, exitHost, serverHost)

	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	registry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: 12,
		Signer: serverKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, serverHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	handler := &echoSignalHandler{seen: make(chan []byte, 1)}
	registration, err := OpenRegistration(context.Background(), RegistrationClientOptions{
		Host: exitHost, Server: serverHost.ID(),
		SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, 1, true),
		Config:           config, Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-registration.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("registration did not observe owner loss")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := registration.Close(ctx); err != nil {
		t.Fatalf("cleanup after owner loss returned operational stream error: %v", err)
	}
}

func TestHeartbeatRenewsReceiptBeforeExpiry(t *testing.T) {
	serverHost, serverKey := newRendezvousHost(t)
	exitHost, exitKey := newRendezvousHost(t)
	connectRendezvousHosts(t, exitHost, serverHost)
	base := time.Unix(1_800_200_000, 0).UTC()
	var clock atomic.Int64
	clock.Store(base.UnixNano())
	config := DefaultConfig()
	config.Now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = 20 * time.Millisecond
	config.RegistrationStale = time.Second
	config.RegistrationExpiry = 2 * time.Second
	config.ReceiptTTL = 2 * time.Second
	config.ReceiptRenewLead = time.Second
	config.Admission.HeartbeatInterval = time.Nanosecond
	config.Admission.HeartbeatBurst = 100
	registry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: 13,
		Signer: serverKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, serverHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	registration, err := OpenRegistration(context.Background(), RegistrationClientOptions{
		Host: exitHost, Server: serverHost.ID(),
		SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), base, 1, 1, true),
		Config:           config, Handler: &echoSignalHandler{seen: make(chan []byte, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = registration.Close(ctx)
	})
	initial := registration.Snapshot()
	if initial.ReceiptSequence != 1 || !initial.ReceiptExpiresAt.Equal(base.Add(2*time.Second)) {
		t.Fatalf("initial receipt=%+v", initial)
	}
	clock.Store(base.Add(time.Second).UnixNano())
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			current := registration.Snapshot()
			if current.ReceiptSequence == 2 && current.ReceiptExpiresAt.Equal(base.Add(3*time.Second)) {
				return
			}
		case <-deadline.C:
			t.Fatalf("receipt was not renewed: %+v", registration.Snapshot())
		}
	}
}

func TestFrameRejectsOversizeBeforePayloadRead(t *testing.T) {
	var input bytes.Buffer
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxFrameSize+1)
	input.Write(header[:])
	if _, err := ReadFrame(&input); err == nil {
		t.Fatal("oversized frame accepted")
	}
}

func TestCandidateRequiresReceiptForEveryOwnerRoute(t *testing.T) {
	now := time.Unix(1_800_100_000, 0)
	registry, _ := testRegistry(t, &now, fixedCountryResolver{country: "DE"})
	exitKey, exitPeer := testIdentity(t)
	registrationID, _ := NewRegistrationID(nil)
	channel, _ := NewChannelToken(nil)
	if _, err := registry.Register(context.Background(), exitPeer, registrationID, channel,
		testSignedExit(t, exitKey, exitPeer, now, 1, 1, true), netip.MustParseAddr("198.51.100.41")); err != nil {
		t.Fatal(err)
	}
	candidates, err := registry.Sample("DE", 1, nil)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("sample candidates=%d err=%v", len(candidates), err)
	}
	unreceiptedServer, _ := newRendezvousHost(t)
	unreceiptedID, _ := NewRegistrationID(nil)
	candidates[0].Routes = append(candidates[0].Routes, OwnerRoute{
		ServerPeer: unreceiptedServer.ID(), ServerIncarnation: 2, RegistrationID: unreceiptedID,
		ControlAddresses: []string{hostControlAddress(t, unreceiptedServer)},
	})
	if _, err := CandidateToProto(candidates[0]); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unreceipted owner route error=%v", err)
	}
}

func TestCandidateRouteCacheEvictsOldestEpoch(t *testing.T) {
	provider := &CandidateProvider{
		routes: make(map[providerRouteKey]Candidate), hops: make(map[providerRouteKey][]brokerHop),
	}
	oldest := providerRouteKey{peer: peer.ID("fixture-peer"), epoch: 1}
	for epoch := uint64(1); epoch <= MaxCandidateRouteCache+1; epoch++ {
		key := providerRouteKey{peer: oldest.peer, epoch: epoch}
		provider.rememberCandidateRouteLocked(key, Candidate{}, []brokerHop{{ingress: peer.ID("fixture-ingress")}})
	}
	if len(provider.routes) != MaxCandidateRouteCache || len(provider.hops) != MaxCandidateRouteCache ||
		len(provider.routeFIFO) != MaxCandidateRouteCache {
		t.Fatalf("cache sizes routes=%d hops=%d fifo=%d", len(provider.routes), len(provider.hops), len(provider.routeFIFO))
	}
	if _, exists := provider.routes[oldest]; exists {
		t.Fatal("oldest candidate route was not evicted")
	}
}

func TestCompatibleM11DescriptorsProduceBoundedPartialBatch(t *testing.T) {
	serverHost, serverKey := newRendezvousHost(t)
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = time.Second
	config.RegistrationStale = 10 * time.Second
	config.RegistrationExpiry = 20 * time.Second
	registry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: 19,
		Signer: serverKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, serverHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	registrations := make([]*RegistrationClient, 0, 3)
	t.Cleanup(func() {
		for _, registration := range registrations {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = registration.Close(ctx)
			cancel()
		}
	})
	for index := range 3 {
		exitHost, exitKey := newRendezvousHost(t)
		connectRendezvousHosts(t, exitHost, serverHost)
		signed := testLargeSignedExit(t, exitKey, exitHost.ID(), time.Now(), MaxFrameSize/3+1024)
		if len(signed) <= PreferredDescriptorSize || len(signed) > MaxDescriptorSize {
			t.Fatalf("descriptor size=%d outside compatibility range", len(signed))
		}
		registration, openErr := OpenRegistration(context.Background(), RegistrationClientOptions{
			Host: exitHost, Server: serverHost.ID(), SignedDescriptor: signed,
			Config: config, Handler: &echoSignalHandler{seen: make(chan []byte, 1)},
		})
		if openErr != nil {
			t.Fatalf("register compatible descriptor %d: %v", index, openErr)
		}
		registrations = append(registrations, registration)
	}

	queryHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, queryHost, serverHost)
	client, err := NewClient(queryHost, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	values, partial, err := client.GetCandidates(ctx, serverHost.ID(), "DE", 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !partial || len(values) == 0 || len(values) >= 3 {
		t.Fatalf("bounded batch count=%d partial=%t", len(values), partial)
	}
}

func testLargeSignedExit(t *testing.T, key crypto.PrivKey, subject peer.ID, now time.Time, minimum int) []byte {
	t.Helper()
	message := &irviov1.NodeAnnouncement{
		ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(subject),
		SessionEpoch: bytes.Repeat([]byte{0x31}, protocol.SessionEpochSize),
		Incarnation:  1, Sequence: 1, Capabilities: protocol.CapDiscovery | protocol.CapExit,
		Country: "DE", EgressIpv4: true, AwgPublicKey: bytes.Repeat([]byte{0x42}, 32), AwgKeyEpoch: 1,
		CreatedAtUnix: now.Add(-time.Minute).Unix(), ExpiresAtUnix: now.Add(5 * time.Minute).Unix(),
		AcceptingNewSessions: true, SessionFeatures: []string{"session-establish-v1", "session-control-v1"},
	}
	for index := range protocol.MaxAnnouncementAddresses {
		message.Libp2PAddrs = append(message.Libp2PAddrs, "/unix/"+strings.Repeat("x", 900)+fmt.Sprint(index))
		signed, err := protocol.SealAnnouncement(key, message, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(signed) >= minimum {
			return signed
		}
	}
	t.Fatalf("could not construct descriptor of at least %d bytes", minimum)
	return nil
}

func TestM12AuthenticatedRendezvousLoad(t *testing.T) {
	if os.Getenv("HYDRA_M12_LOAD") != "1" {
		t.Skip("set HYDRA_M12_LOAD=1 for the real authenticated protocol load test")
	}
	const registrations = 100
	const requests = 1_000
	const signals = 100

	serverHost, serverKey := newRendezvousHost(t)
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = time.Second
	config.RegistrationStale = 10 * time.Second
	config.RegistrationExpiry = 20 * time.Second
	config.Admission.GETInterval = time.Nanosecond
	config.Admission.GETBurst = 100
	config.Admission.SignalInterval = time.Nanosecond
	config.Admission.SignalBurst = 100
	registry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: 700,
		Signer: serverKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, serverHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	registrationsOpen := make([]*RegistrationClient, 0, registrations)
	for index := range registrations {
		exitHost, exitKey := newRendezvousHost(t)
		connectRendezvousHosts(t, exitHost, serverHost)
		registration, openErr := OpenRegistration(context.Background(), RegistrationClientOptions{
			Host: exitHost, Server: serverHost.ID(),
			SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, uint64(index+1), true),
			Config:           config, Handler: &echoSignalHandler{seen: make(chan []byte, 1)},
		})
		if openErr != nil {
			t.Fatal(openErr)
		}
		registrationsOpen = append(registrationsOpen, registration)
	}
	t.Cleanup(func() {
		for _, registration := range registrationsOpen {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = registration.Close(ctx)
			cancel()
		}
	})

	clientHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, clientHost, serverHost)
	client, err := NewClient(clientHost, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	beforeFDs := processFDCount()
	latencies := make([]time.Duration, 0, requests+signals)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var candidate Candidate
	for range requests {
		started := time.Now()
		values, _, getErr := client.GetCandidates(ctx, serverHost.ID(), "DE", 10, nil)
		latencies = append(latencies, time.Since(started))
		if getErr != nil || len(values) == 0 {
			t.Fatalf("GET candidates=%d err=%v", len(values), getErr)
		}
		candidate = values[0]
	}
	for range signals {
		request, requestErr := NewRequestID(nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		route := candidate.Routes[0]
		started := time.Now()
		_, signalErr := client.Signal(ctx, serverHost.ID(), &irviov1.RendezvousSignal{
			ProtocolVersion: ProtocolVersion, RequestHandle: request[:], TargetPeerId: []byte(candidate.Descriptor.Subject),
			OwnerRoute: &irviov1.OwnerRoute{
				ServerPeerId: []byte(route.ServerPeer), ServerIncarnation: route.ServerIncarnation,
				RegistrationId: route.RegistrationID[:], ControlAddresses: route.ControlAddresses,
			},
			SignedMessage: []byte("bounded-load-signal"), DeadlineUnixMilli: time.Now().Add(2 * time.Second).UnixMilli(),
		})
		latencies = append(latencies, time.Since(started))
		if signalErr != nil {
			t.Fatal(signalErr)
		}
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	snapshot := registry.Snapshot()
	t.Logf(
		"M12_REAL_LOAD registrations=%d fresh=%d requests=%d signals=%d p50=%s p95=%s p99=%s heap_alloc=%d heap_delta=%d gc_delta=%d fds=%d fd_delta=%d",
		registrations, snapshot.Fresh, requests, signals,
		loadPercentile(latencies, 50), loadPercentile(latencies, 95), loadPercentile(latencies, 99),
		after.HeapAlloc, int64(after.HeapAlloc)-int64(before.HeapAlloc), after.NumGC-before.NumGC,
		processFDCount(), processFDCount()-beforeFDs,
	)
}

func loadPercentile(values []time.Duration, percentile int) time.Duration {
	index := (len(values)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > len(values) {
		index = len(values)
	}
	return values[index-1]
}

func processFDCount() int {
	values, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(values)
}

func TestCandidateProviderProgressesFiveToTenWithinFourGETs(t *testing.T) {
	serverHosts := make([]host.Host, 0, 2)
	serverKeys := make([]crypto.PrivKey, 0, 2)
	services := make([]*Service, 0, 2)
	registrations := make([]*RegistrationClient, 0, 10)
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = 100 * time.Millisecond
	config.RegistrationStale = time.Second
	config.RegistrationExpiry = 2 * time.Second
	for index := range 2 {
		serverHost, serverKey := newRendezvousHost(t)
		serverHosts = append(serverHosts, serverHost)
		serverKeys = append(serverKeys, serverKey)
		registry, err := NewRegistry(RegistryOptions{
			Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: uint64(70 + index),
			Signer: serverKey, Resolver: fixedCountryResolver{country: "DE"},
			ControlAddresses: []string{hostControlAddress(t, serverHost)},
		})
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, service)
	}
	defer func() {
		for _, registration := range registrations {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = registration.Close(ctx)
			cancel()
		}
		for _, service := range services {
			_ = service.Close()
		}
	}()
	for index := range 10 {
		exitHost, exitKey := newRendezvousHost(t)
		server := serverHosts[index%2]
		connectRendezvousHosts(t, exitHost, server)
		registration, err := OpenRegistration(context.Background(), RegistrationClientOptions{
			Host: exitHost, Server: server.ID(),
			SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, 1, true),
			Config:           config, Handler: &echoSignalHandler{seen: make(chan []byte, 1)},
		})
		if err != nil {
			t.Fatal(err)
		}
		registrations = append(registrations, registration)
	}
	clientHost, _ := newRendezvousHost(t)
	for _, server := range serverHosts {
		connectRendezvousHosts(t, clientHost, server)
	}
	client, _ := NewClient(clientHost, 3*time.Second)
	forwarder, _ := NewVerifiedForwarder(client, 32)
	provider, err := NewCandidateProvider(clientHost.ID(), client, StaticServerProvider{
		Peers: []peer.ID{serverHosts[0].ID(), serverHosts[1].ID()},
	}, forwarder)
	if err != nil {
		t.Fatal(err)
	}
	request := exitselector.CandidateRequest{Country: "DE", Limit: 10}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := provider.GetCandidates(ctx, request)
	if err != nil || len(first.Candidates) != 5 {
		t.Fatalf("initial batch=%d err=%v", len(first.Candidates), err)
	}
	second, err := provider.GetCandidates(ctx, request)
	if err != nil || len(second.Candidates) != 10 {
		t.Fatalf("progressive batch=%d err=%v", len(second.Candidates), err)
	}
	if provider.roundRPCs(provider.rounds["DE"]) > MaxAcquisitionRPCs {
		t.Fatalf("GET RPCs=%d", provider.roundRPCs(provider.rounds["DE"]))
	}
	probe := MetadataProbe{Provider: provider}
	result, err := probe.Probe(ctx, second.Candidates[0])
	if err != nil || !result.Reachable || result.Kind != exitselector.ProbeBrokerMetadata || result.Latency != 0 {
		t.Fatalf("metadata probe=%+v err=%v", result, err)
	}
	selection, err := exitselector.EvaluateBatch(ctx, second, probe, exitselector.EvaluationConfig{
		LocalPeer: clientHost.ID(), Country: "DE", Now: time.Now(),
		ProbeConcurrency: 5, ProbeTimeout: time.Second, LatencyTolerance: 5 * time.Millisecond,
	})
	if err != nil || selection.Active == nil || selection.Reserve == nil || selection.Cold == nil {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
}

func TestCandidateProviderSignalsThroughCandidateSourceFallback(t *testing.T) {
	primaryHost, primaryKey := newRendezvousHost(t)
	fallbackHost, fallbackKey := newRendezvousHost(t)
	deadIngress, _ := newRendezvousHost(t)
	exitHost, exitKey := newRendezvousHost(t)
	clientHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, clientHost, primaryHost)
	connectRendezvousHosts(t, clientHost, fallbackHost)
	connectRendezvousHosts(t, clientHost, deadIngress)
	connectRendezvousHosts(t, exitHost, fallbackHost)
	deadIngress.SetStreamHandler(libprotocol.ID(ProtocolID), func(stream network.Stream) {
		defer stream.Reset()
		time.Sleep(2 * time.Second)
	})

	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = time.Second
	config.RegistrationStale = 10 * time.Second
	config.RegistrationExpiry = 20 * time.Second
	services := make([]*Service, 0, 2)
	for index, fixture := range []struct {
		host host.Host
		key  crypto.PrivKey
	}{{primaryHost, primaryKey}, {fallbackHost, fallbackKey}} {
		registry, err := NewRegistry(RegistryOptions{
			Config: config, ServerPeer: fixture.host.ID(), ServerIncarnation: uint64(900 + index),
			Signer: fixture.key, Resolver: fixedCountryResolver{country: "DE"},
			ControlAddresses: []string{hostControlAddress(t, fixture.host)},
		})
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewService(ServiceOptions{Host: fixture.host, Registry: registry, StreamTimeout: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, service)
	}
	t.Cleanup(func() {
		for _, service := range services {
			_ = service.Close()
		}
	})
	handler := &echoSignalHandler{seen: make(chan []byte, 1)}
	registration, err := OpenRegistration(context.Background(), RegistrationClientOptions{
		Host: exitHost, Server: fallbackHost.ID(),
		SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, 1, true),
		Config:           config, Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = registration.Close(ctx)
	})

	client, err := NewClient(clientHost, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	forwarder, err := NewVerifiedForwarder(client, 16)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewCandidateProvider(clientHost.ID(), client, StaticServerProvider{
		Peers: []peer.ID{primaryHost.ID(), fallbackHost.ID()},
	}, forwarder)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	batch, err := provider.GetCandidates(ctx, exitselector.CandidateRequest{Country: "DE", Limit: 5})
	if err != nil || len(batch.Candidates) != 1 || batch.Candidates[0].PeerID != exitHost.ID() {
		t.Fatalf("fallback batch=%+v err=%v", batch, err)
	}
	if got := provider.roundRPCs(provider.rounds["DE"]); got != 2 {
		t.Fatalf("initial usable partial batch performed synchronous top-up: RPCs=%d, want 2", got)
	}
	carrier, err := provider.BrokerCarrier(batch.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	response, err := carrier.Exchange(ctx, exitHost.ID(), []byte("fallback-only-route"))
	if err != nil || string(response) != "response:fallback-only-route" {
		t.Fatalf("fallback signaling response=%q err=%v", response, err)
	}
	if messages, _ := carrier.Usage(); messages != 2 || handler.calls.Load() != 1 {
		t.Fatalf("messages=%d handler calls=%d", messages, handler.calls.Load())
	}
	resolved, err := provider.Resolve(batch.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	receipt, exists := candidateReceipt(resolved, resolved.Routes[0])
	if !exists {
		t.Fatal("fallback route receipt missing")
	}
	retryCarrier, err := newBrokerCarrier(client, resolved, []brokerHop{
		{ingress: primaryHost.ID(), owner: resolved.Routes[0], receipt: receipt},
		{ingress: fallbackHost.ID(), owner: resolved.Routes[0], receipt: receipt},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err = retryCarrier.Exchange(ctx, exitHost.ID(), []byte("retry-route"))
	if err != nil || string(response) != "response:retry-route" {
		t.Fatalf("retried signaling response=%q err=%v", response, err)
	}
	if messages, _ := retryCarrier.Usage(); messages != 3 || handler.calls.Load() != 2 {
		t.Fatalf("retried messages=%d handler calls=%d", messages, handler.calls.Load())
	}

	// PREPARE/OFFER share one budget; retry each stale ingress at most once.
	handler.delay.Store(int64(300 * time.Millisecond))
	budgetCarrier, err := newBrokerCarrier(client, resolved, []brokerHop{
		{ingress: deadIngress.ID(), owner: resolved.Routes[0], receipt: receipt},
		{ingress: fallbackHost.ID(), owner: resolved.Routes[0], receipt: receipt},
	})
	if err != nil {
		t.Fatal(err)
	}
	budgetContext, budgetCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer budgetCancel()
	if _, err := budgetCarrier.Exchange(budgetContext, exitHost.ID(), []byte("prepare")); err != nil {
		t.Fatalf("PREPARE did not reach alternate route: %v", err)
	}
	if _, err := budgetCarrier.Exchange(budgetContext, exitHost.ID(), []byte("offer")); err != nil {
		t.Fatalf("OFFER retried known-dead route and exhausted fast budget: %v", err)
	}

	allDeadCarrier, err := newBrokerCarrier(client, resolved, []brokerHop{
		{ingress: deadIngress.ID(), owner: resolved.Routes[0], receipt: receipt},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadContext, deadCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer deadCancel()
	if _, err := allDeadCarrier.Exchange(deadContext, exitHost.ID(), []byte("all-dead")); !errors.Is(err, ErrRouteFailed) {
		t.Fatalf("all-routes-dead error = %v", err)
	}
	retryStarted := time.Now()
	if _, err := allDeadCarrier.Exchange(context.Background(), exitHost.ID(), []byte("bounded-retry")); !errors.Is(err, ErrRouteFailed) {
		t.Fatalf("known-dead route retry error = %v", err)
	}
	if elapsed := time.Since(retryStarted); elapsed > 50*time.Millisecond {
		t.Fatalf("known-dead route was retried for %s", elapsed)
	}
}

func TestThirteenRendezvousDiscoverFourCountryPoolsWithoutFullMembership(t *testing.T) {
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = time.Second
	config.RegistrationStale = 10 * time.Second
	config.RegistrationExpiry = 20 * time.Second
	config.Admission.GETBurst = 10

	servers := make([]host.Host, 0, 13)
	keys := make([]crypto.PrivKey, 0, 13)
	for range 13 {
		server, key := newRendezvousHost(t)
		servers = append(servers, server)
		keys = append(keys, key)
	}
	ingress := servers[0]
	owners := map[string]int{"DE": 2, "NL": 5, "CA": 8, "ZZ": 12}
	provider := make(countryServerFixture, len(owners))
	for country, index := range owners {
		provider[country] = []peer.ID{servers[index].ID()}
		connectRendezvousHosts(t, ingress, servers[index])
	}

	client, err := NewClient(ingress, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	forwarder, err := NewVerifiedForwarder(client, 64)
	if err != nil {
		t.Fatal(err)
	}
	sampler, err := NewFanoutSampler(FanoutSamplerOptions{
		Local: ingress.ID(), Client: client, Provider: provider, Forwarder: forwarder, Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	services := make([]*Service, 0, len(servers))
	registrationsOpen := make([]*RegistrationClient, 0, len(owners))
	for index, server := range servers {
		country := "DE"
		for label, ownerIndex := range owners {
			if ownerIndex == index {
				country = label
				break
			}
		}
		registry, registryErr := NewRegistry(RegistryOptions{
			Config: config, ServerPeer: server.ID(), ServerIncarnation: uint64(800 + index),
			Signer: keys[index], Resolver: fixedCountryResolver{country: country},
			ControlAddresses: []string{hostControlAddress(t, server)},
		})
		if registryErr != nil {
			t.Fatal(registryErr)
		}
		options := ServiceOptions{
			Host: server, Registry: registry, StreamTimeout: 3 * time.Second,
			AuthorizeSample: allowSampleFrom(ingress.ID()),
		}
		if index == 0 {
			options.RemoteSampler = sampler
		}
		service, serviceErr := NewService(options)
		if serviceErr != nil {
			t.Fatal(serviceErr)
		}
		services = append(services, service)
	}
	t.Cleanup(func() {
		for _, registration := range registrationsOpen {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = registration.Close(ctx)
			cancel()
		}
		for _, service := range services {
			_ = service.Close()
		}
	})

	expected := make(map[string]peer.ID, len(owners))
	for country, index := range owners {
		exitHost, exitKey := newRendezvousHost(t)
		connectRendezvousHosts(t, exitHost, servers[index])
		registration, openErr := OpenRegistration(context.Background(), RegistrationClientOptions{
			Host: exitHost, Server: servers[index].ID(),
			SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, 1, true),
			Config:           config, Handler: &echoSignalHandler{seen: make(chan []byte, 1)},
		})
		if openErr != nil {
			t.Fatal(openErr)
		}
		registrationsOpen = append(registrationsOpen, registration)
		expected[country] = exitHost.ID()
	}

	queryHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, queryHost, ingress)
	query, err := NewClient(queryHost, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, country := range []string{"DE", "NL", "CA", "ZZ"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		values, _, queryErr := query.GetCandidates(ctx, ingress.ID(), country, 5, nil)
		cancel()
		if queryErr != nil || len(values) != 1 || values[0].Descriptor.Subject != expected[country] {
			t.Fatalf("country=%s candidates=%v expected=%s err=%v", country, values, expected[country], queryErr)
		}
	}
	if snapshot := sampler.Snapshot(); snapshot.RPCs != 4 || snapshot.Failures != 0 || snapshot.InFlight != 0 {
		t.Fatalf("fanout snapshot=%+v", snapshot)
	}
}

func TestRegistrationManagerUsesMinThreeAndPreservesDistinctOwners(t *testing.T) {
	for serverCount := 1; serverCount <= 3; serverCount++ {
		t.Run(fmt.Sprint("servers-", serverCount), func(t *testing.T) {
			config := DefaultConfig()
			config.ObservedIPPolicy.AllowLoopback = true
			config.HeartbeatInterval = 100 * time.Millisecond
			config.RegistrationStale = time.Second
			config.RegistrationExpiry = 2 * time.Second
			servers := make([]host.Host, 0, serverCount)
			services := make([]*Service, 0, serverCount)
			serverIDs := make([]peer.ID, 0, serverCount)
			for index := 0; index < serverCount; index++ {
				serverHost, serverKey := newRendezvousHost(t)
				servers = append(servers, serverHost)
				serverIDs = append(serverIDs, serverHost.ID())
				registry, err := NewRegistry(RegistryOptions{
					Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: uint64(90 + index),
					Signer: serverKey, Resolver: fixedCountryResolver{country: "DE"},
					ControlAddresses: []string{hostControlAddress(t, serverHost)},
				})
				if err != nil {
					t.Fatal(err)
				}
				service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				services = append(services, service)
			}
			defer func() {
				for _, service := range services {
					_ = service.Close()
				}
			}()
			exitHost, exitKey := newRendezvousHost(t)
			for _, server := range servers {
				connectRendezvousHosts(t, exitHost, server)
			}
			manager, err := NewRegistrationManager(RegistrationManagerOptions{
				Host: exitHost, Servers: StaticServerProvider{Peers: serverIDs},
				SignedDescriptor: func() []byte {
					return testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, 1, true)
				},
				Handler: &echoSignalHandler{seen: make(chan []byte, 1)},
				Config:  config, OperationTimeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := manager.Start(ctx); err != nil {
				t.Fatal(err)
			}
			for {
				select {
				case snapshot := <-manager.Events():
					if snapshot.Active == serverCount {
						seen := make(map[peer.ID]struct{}, len(snapshot.Servers))
						for _, server := range snapshot.Servers {
							seen[server] = struct{}{}
						}
						if len(seen) != serverCount {
							t.Fatalf("duplicate registration owners: %+v", snapshot.Servers)
						}
						if err := manager.Close(context.Background()); err != nil {
							t.Fatal(err)
						}
						return
					}
				case <-ctx.Done():
					t.Fatalf("active registrations=%d: %v", manager.Snapshot().Active, ctx.Err())
				}
			}
		})
	}
}

func TestRegistrationManagerRefreshesDescriptorWithoutExternalWake(t *testing.T) {
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = 100 * time.Millisecond
	config.RegistrationStale = time.Second
	config.RegistrationExpiry = 2 * time.Second
	config.DescriptorTTL = 8 * time.Second
	config.ReceiptTTL = 8 * time.Second
	config.ReceiptRenewLead = 4 * time.Second
	config.Admission.RegisterInterval = 10 * time.Second

	servers := make([]host.Host, 0, MaxExitRegistrations)
	services := make([]*Service, 0, MaxExitRegistrations)
	registries := make([]*Registry, 0, MaxExitRegistrations)
	serverIDs := make([]peer.ID, 0, MaxExitRegistrations)
	for index := 0; index < MaxExitRegistrations; index++ {
		serverHost, serverKey := newRendezvousHost(t)
		servers = append(servers, serverHost)
		serverIDs = append(serverIDs, serverHost.ID())
		registry, err := NewRegistry(RegistryOptions{
			Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: uint64(190 + index),
			Signer: serverKey, Resolver: fixedCountryResolver{country: "DE"},
			ControlAddresses: []string{hostControlAddress(t, serverHost)},
		})
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewService(ServiceOptions{Host: serverHost, Registry: registry, StreamTimeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		registries = append(registries, registry)
		services = append(services, service)
	}
	defer func() {
		for _, service := range services {
			_ = service.Close()
		}
	}()

	exitHost, exitKey := newRendezvousHost(t)
	for _, server := range servers {
		connectRendezvousHosts(t, exitHost, server)
	}
	descriptor := atomic.Value{}
	descriptor.Store(testShortSignedExit(t, exitKey, exitHost.ID(), 1, config.DescriptorTTL))
	manager, err := NewRegistrationManager(RegistrationManagerOptions{
		Host: exitHost, Servers: StaticServerProvider{Peers: serverIDs},
		SignedDescriptor: func() []byte {
			return append([]byte(nil), descriptor.Load().([]byte)...)
		},
		Handler: &echoSignalHandler{seen: make(chan []byte, 1)},
		Config:  config, OperationTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close(context.Background()) }()
	waitRegistrationManagerState(t, ctx, manager, registries, 1)

	// Periodic renewal must refresh the descriptor without a DescriptorChanged notification.
	descriptor.Store(testShortSignedExit(t, exitKey, exitHost.ID(), 2, config.DescriptorTTL))
	waitRegistrationManagerState(t, ctx, manager, registries, 2)
	descriptor.Store(testShortSignedExit(t, exitKey, exitHost.ID(), 3, config.DescriptorTTL))
	waitRegistrationManagerState(t, ctx, manager, registries, 3)
}

func testShortSignedExit(t *testing.T, key crypto.PrivKey, subject peer.ID, sequence uint64, ttl time.Duration) []byte {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	message := &irviov1.NodeAnnouncement{
		ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(subject),
		SessionEpoch: bytes.Repeat([]byte{0x33}, protocol.SessionEpochSize),
		Incarnation:  1, Sequence: sequence, Capabilities: protocol.CapDiscovery | protocol.CapExit,
		Country: "DE", EgressIpv4: true, AwgPublicKey: bytes.Repeat([]byte{0x44}, 32), AwgKeyEpoch: 1,
		CreatedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(ttl).Unix(), AcceptingNewSessions: true,
		SessionFeatures: []string{"session-establish-v1", "session-control-v1"},
	}
	signed, err := protocol.SealAnnouncement(key, message, now)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func waitRegistrationManagerState(t *testing.T, ctx context.Context, manager *RegistrationManager, registries []*Registry, sequence uint64) {
	t.Helper()
	for {
		select {
		case <-manager.Events():
			matched := 0
			for _, registry := range registries {
				candidates, err := registry.Sample("DE", 1, nil)
				if err == nil && len(candidates) == 1 && candidates[0].Descriptor.Version.Sequence == sequence {
					matched++
				}
			}
			if matched == len(registries) {
				return
			}
		case <-ctx.Done():
			installed := make([]uint64, 0, len(registries))
			for _, registry := range registries {
				candidates, _ := registry.Sample("DE", 1, nil)
				if len(candidates) == 1 {
					installed = append(installed, candidates[0].Descriptor.Version.Sequence)
				} else {
					installed = append(installed, 0)
				}
			}
			t.Fatalf("descriptor sequence %d was not installed on all owners; installed=%v: %v", sequence, installed, ctx.Err())
		}
	}
}

func TestTwoBrokerSamplingAndSignalingDoesNotRecurse(t *testing.T) {
	ingressHost, ingressKey := newRendezvousHost(t)
	ownerHost, ownerKey := newRendezvousHost(t)
	exitHost, exitKey := newRendezvousHost(t)
	clientHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, ingressHost, ownerHost)
	connectRendezvousHosts(t, exitHost, ownerHost)
	connectRendezvousHosts(t, clientHost, ingressHost)

	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = 100 * time.Millisecond
	config.RegistrationStale = time.Second
	config.RegistrationExpiry = 2 * time.Second
	ownerRegistry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: ownerHost.ID(), ServerIncarnation: 21,
		Signer: ownerKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, ownerHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerService, err := NewService(ServiceOptions{
		Host: ownerHost, Registry: ownerRegistry, StreamTimeout: 3 * time.Second,
		AuthorizeSample: allowSampleFrom(ingressHost.ID()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ownerService.Close()
	handler := &echoSignalHandler{seen: make(chan []byte, 1)}
	registration, err := OpenRegistration(context.Background(), RegistrationClientOptions{
		Host: exitHost, Server: ownerHost.ID(),
		SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, 1, true),
		Config:           config, Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = registration.Close(ctx)
	}()

	ingressRegistry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: ingressHost.ID(), ServerIncarnation: 31,
		Signer: ingressKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, ingressHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ingressClient, _ := NewClient(ingressHost, 3*time.Second)
	forwarder, _ := NewVerifiedForwarder(ingressClient, 32)
	fanout, err := NewFanoutSampler(FanoutSamplerOptions{
		Local: ingressHost.ID(), Client: ingressClient,
		Provider:  StaticServerProvider{Peers: []peer.ID{ownerHost.ID()}},
		Forwarder: forwarder, Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	ingressService, err := NewService(ServiceOptions{
		Host: ingressHost, Registry: ingressRegistry, Forwarder: forwarder,
		RemoteSampler: fanout, StreamTimeout: 3 * time.Second,
		AuthorizeSample: allowSampleFrom(ownerHost.ID()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ingressService.Close()

	client, _ := NewClient(clientHost, 3*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	candidates, _, err := client.GetCandidates(ctx, ingressHost.ID(), "DE", 5, nil)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("distributed candidates=%d err=%v", len(candidates), err)
	}
	if snapshot := fanout.Snapshot(); snapshot.RPCs != 1 {
		t.Fatalf("fanout RPCs=%d, want 1", snapshot.RPCs)
	}
	if _, _, err := client.SampleLocal(ctx, ingressHost.ID(), "DE", 5, nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ordinary peer SAMPLE_LOCAL error=%v", err)
	}
	ownerClient, _ := NewClient(ownerHost, 3*time.Second)
	localOnly, _, err := ownerClient.SampleLocal(ctx, ingressHost.ID(), "DE", 5, nil)
	if err != nil || len(localOnly) != 0 {
		t.Fatalf("SAMPLE_LOCAL returned %d, %v", len(localOnly), err)
	}
	if snapshot := fanout.Snapshot(); snapshot.RPCs != 1 {
		t.Fatalf("SAMPLE_LOCAL recursed; RPCs=%d", snapshot.RPCs)
	}

	request, _ := NewRequestID(nil)
	route := candidates[0].Routes[0]
	response, err := client.Signal(ctx, ingressHost.ID(), &irviov1.RendezvousSignal{
		ProtocolVersion: ProtocolVersion, RequestHandle: append([]byte(nil), request[:]...),
		TargetPeerId: []byte(exitHost.ID()),
		OwnerRoute: &irviov1.OwnerRoute{
			ServerPeerId: []byte(route.ServerPeer), ServerIncarnation: route.ServerIncarnation,
			RegistrationId:   append([]byte(nil), route.RegistrationID[:]...),
			ControlAddresses: append([]string(nil), route.ControlAddresses...),
		},
		SignedMessage:     []byte("two-broker-prepare"),
		DeadlineUnixMilli: time.Now().Add(2 * time.Second).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(response.SignedMessage) != "response:two-broker-prepare" {
		t.Fatalf("two-broker response=%q", response.SignedMessage)
	}
}

func TestSignedEstablishmentAcrossTwoBrokers(t *testing.T) {
	ingressHost, ingressKey := newRendezvousHost(t)
	ownerHost, ownerKey := newRendezvousHost(t)
	exitHost, exitKey := newRendezvousHost(t)
	clientHost, clientKey := newRendezvousHost(t)
	connectRendezvousHosts(t, ingressHost, ownerHost)
	connectRendezvousHosts(t, exitHost, ownerHost)
	connectRendezvousHosts(t, clientHost, ingressHost)

	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = 100 * time.Millisecond
	config.RegistrationStale = time.Second
	config.RegistrationExpiry = 2 * time.Second
	ownerRegistry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: ownerHost.ID(), ServerIncarnation: 41,
		Signer: ownerKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, ownerHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerService, err := NewService(ServiceOptions{
		Host: ownerHost, Registry: ownerRegistry, StreamTimeout: 3 * time.Second,
		AuthorizeSample: allowSampleFrom(ingressHost.ID()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ownerService.Close()

	now := time.Now().Truncate(time.Millisecond)
	signedDescriptor := testSignedExit(t, exitKey, exitHost.ID(), now, 1, 1, true)
	verifiedDescriptor, err := VerifyDescriptor(signedDescriptor, exitHost.ID(), now)
	if err != nil {
		t.Fatal(err)
	}
	binding := sessionestablish.DescriptorBinding{
		SignedEnvelope: signedDescriptor, ID: verifiedDescriptor.ID,
		Incarnation: 1, Sequence: 1, AWGKeyEpoch: 1,
	}
	copy(binding.AWGPublicKey[:], verifiedDescriptor.Announcement.AwgPublicKey)
	clientSigned := testSignedExit(t, clientKey, clientHost.ID(), now, 1, 1, true)
	responder, err := sessionestablish.NewResponder(sessionestablish.ResponderOptions{
		Local: exitHost.ID(), PrivateKey: exitKey,
		Descriptor:        func() sessionestablish.DescriptorBinding { return binding },
		NetworkGeneration: func() uint64 { return 2 },
		Authorizer:        sessionestablish.AllowCompatiblePeer{},
		Processor:         brokerOfferProcessor{exit: exitHost.ID(), now: now},
		Policy:            sessionestablish.DefaultPolicy(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer responder.Close()
	registration, err := OpenRegistration(context.Background(), RegistrationClientOptions{
		Host: exitHost, Server: ownerHost.ID(), SignedDescriptor: signedDescriptor,
		Config: config, Handler: responder,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = registration.Close(ctx)
	}()

	ingressRegistry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: ingressHost.ID(), ServerIncarnation: 51,
		Signer: ingressKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, ingressHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ingressClient, _ := NewClient(ingressHost, 3*time.Second)
	forwarder, _ := NewVerifiedForwarder(ingressClient, 32)
	fanout, _ := NewFanoutSampler(FanoutSamplerOptions{
		Local: ingressHost.ID(), Client: ingressClient,
		Provider:  StaticServerProvider{Peers: []peer.ID{ownerHost.ID()}},
		Forwarder: forwarder, Concurrency: 2,
	})
	ingressService, err := NewService(ServiceOptions{
		Host: ingressHost, Registry: ingressRegistry, Forwarder: forwarder,
		RemoteSampler: fanout, StreamTimeout: 3 * time.Second,
		AuthorizeSample: allowSampleFrom(ownerHost.ID()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ingressService.Close()

	client, _ := NewClient(clientHost, 3*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	candidates, _, err := client.GetCandidates(ctx, ingressHost.ID(), "DE", 5, nil)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates=%d err=%v", len(candidates), err)
	}
	carrier, err := NewBrokerCarrier(client, ingressHost.ID(), candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	initiator, err := sessionestablish.NewInitiator(sessionestablish.InitiatorOptions{
		Local: clientHost.ID(), PrivateKey: clientKey, LocalAWGKeyEpoch: 1,
		SignedAnnouncement: clientSigned,
		NetworkGeneration:  1, RuntimeGeneration: 1, AWGProfileHash: [32]byte{1},
		Policy: sessionestablish.DefaultPolicy(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := initiator.Establish(ctx, carrier, exitHost.ID(), "DE", binding,
		sessionestablish.OfferFactoryFunc(func(context.Context, sessionestablish.ReadyContext) (*irviov1.IceOffer, error) {
			return &irviov1.IceOffer{
				ProtocolVersion: 1, SessionId: bytes.Repeat([]byte{4}, 16),
				InitiatorPeerId: []byte(clientHost.ID()), ResponderPeerId: []byte(exitHost.ID()),
				Purpose:       irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT,
				Role:          irviov1.IceRole_ICE_ROLE_CONTROLLING,
				Description:   testICEDescription([]byte{10, 0, 0, 1}, 50001, "client"),
				CreatedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(time.Minute).Unix(),
			}, nil
		}), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Body.ProvisionalGrant == nil {
		t.Fatal("missing provisional grant")
	}
	if messages, usedBytes := carrier.Usage(); messages != 4 || usedBytes > MaxSignalingBytes {
		t.Fatalf("carrier usage messages=%d bytes=%d", messages, usedBytes)
	}
}

func TestBrokerRoutesMultipleExitsOwnedByOneRendezvous(t *testing.T) {
	ingressHost, ingressKey := newRendezvousHost(t)
	ownerHost, ownerKey := newRendezvousHost(t)
	clientHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, ingressHost, ownerHost)
	connectRendezvousHosts(t, clientHost, ingressHost)

	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval = time.Second
	config.RegistrationStale = 10 * time.Second
	config.RegistrationExpiry = 20 * time.Second
	ownerRegistry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: ownerHost.ID(), ServerIncarnation: 61,
		Signer: ownerKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, ownerHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerService, err := NewService(ServiceOptions{
		Host: ownerHost, Registry: ownerRegistry, StreamTimeout: 3 * time.Second,
		AuthorizeSample: allowSampleFrom(ingressHost.ID()),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ownerService.Close() })

	registrations := make([]*RegistrationClient, 0, 2)
	handlers := make(map[peer.ID]*echoSignalHandler, 2)
	for index := range 2 {
		exitHost, exitKey := newRendezvousHost(t)
		connectRendezvousHosts(t, exitHost, ownerHost)
		handler := &echoSignalHandler{seen: make(chan []byte, 1)}
		registration, openErr := OpenRegistration(context.Background(), RegistrationClientOptions{
			Host: exitHost, Server: ownerHost.ID(),
			SignedDescriptor: testSignedExit(t, exitKey, exitHost.ID(), time.Now(), 1, uint64(index+1), true),
			Config:           config, Handler: handler,
		})
		if openErr != nil {
			t.Fatal(openErr)
		}
		registrations = append(registrations, registration)
		handlers[exitHost.ID()] = handler
	}
	t.Cleanup(func() {
		for _, registration := range registrations {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = registration.Close(ctx)
			cancel()
		}
	})

	ingressRegistry, err := NewRegistry(RegistryOptions{
		Config: config, ServerPeer: ingressHost.ID(), ServerIncarnation: 62,
		Signer: ingressKey, Resolver: fixedCountryResolver{country: "DE"},
		ControlAddresses: []string{hostControlAddress(t, ingressHost)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ingressClient, err := NewClient(ingressHost, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	forwarder, err := NewVerifiedForwarder(ingressClient, 16)
	if err != nil {
		t.Fatal(err)
	}
	sampler, err := NewFanoutSampler(FanoutSamplerOptions{
		Local: ingressHost.ID(), Client: ingressClient,
		Provider:  StaticServerProvider{Peers: []peer.ID{ownerHost.ID()}},
		Forwarder: forwarder, Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	ingressService, err := NewService(ServiceOptions{
		Host: ingressHost, Registry: ingressRegistry, Forwarder: forwarder,
		RemoteSampler: sampler, StreamTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ingressService.Close() })

	client, err := NewClient(clientHost, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	candidates, _, err := client.GetCandidates(ctx, ingressHost.ID(), "DE", 5, nil)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("candidates=%d err=%v", len(candidates), err)
	}
	for _, candidate := range candidates {
		carrier, carrierErr := NewBrokerCarrier(client, ingressHost.ID(), candidate)
		if carrierErr != nil {
			t.Fatal(carrierErr)
		}
		payload := []byte("route-" + candidate.Descriptor.Subject.String())
		response, exchangeErr := carrier.Exchange(ctx, candidate.Descriptor.Subject, payload)
		if exchangeErr != nil || !bytes.Equal(response, append([]byte("response:"), payload...)) {
			t.Fatalf("subject=%s response=%q err=%v", candidate.Descriptor.Subject, response, exchangeErr)
		}
		if handlers[candidate.Descriptor.Subject].calls.Load() != 1 {
			t.Fatalf("subject=%s calls=%d", candidate.Descriptor.Subject, handlers[candidate.Descriptor.Subject].calls.Load())
		}
	}
}

func allowSampleFrom(ids ...peer.ID) SampleAuthorizer {
	allowed := make(map[peer.ID]struct{}, len(ids))
	for _, id := range ids {
		allowed[id] = struct{}{}
	}
	return func(_ context.Context, remote peer.ID) error {
		if _, ok := allowed[remote]; !ok {
			return ErrUnauthorized
		}
		return nil
	}
}

func TestProductionObservedIPPolicyRejectsDocumentation(t *testing.T) {
	address, _ := netip.AddrFromSlice(net.ParseIP("198.51.100.1").To4())
	if validObservedAddress(address, ObservedIPPolicy{}) {
		t.Fatal("documentation address accepted by production policy")
	}
}

func TestRegistrationErrorClassIsBoundedAndSecretFree(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "canceled"},
		{ErrUnauthorized, "unauthorized"},
		{ErrRateLimited, "rate_limited"},
		{ErrBusy, "busy"},
		{ErrWrongPeer, "wrong_peer"},
		{ErrConflict, "conflict"},
		{ErrInvalid, "invalid"},
		{errors.New("secret-bearing transport detail"), "transport"},
	}
	for _, test := range tests {
		if got := registrationErrorClass(test.err); got != test.want {
			t.Fatalf("registrationErrorClass(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
