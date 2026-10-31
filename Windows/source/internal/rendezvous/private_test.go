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
	"net/netip"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/proto"
)

func privateMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func signedPrivateExit(t *testing.T, key crypto.PrivKey, id peer.ID, now time.Time, sequence uint64) []byte {
	t.Helper()
	signed := testSignedExit(t, key, id, now, 1, sequence, true)
	verified, err := protocol.VerifySignedAnnouncementRecord(signed, id, now)
	privateMust(t, err)
	message := verified.Announcement
	message.Capabilities = protocol.CapDiscovery | protocol.CapPrivateExit
	message.Country = ""
	message.SessionFeatures = append(message.SessionFeatures, protocol.PrivateExitFeature)
	signed, err = protocol.SealAnnouncement(key, message, now)
	privateMust(t, err)
	return signed
}

func privateRegistryFixture(t *testing.T, now *time.Time) (*Registry, peer.ID) {
	t.Helper()
	key, server := testIdentity(t)
	config := DefaultConfig()
	config.MaxRegistrations = 1
	config.Now = func() time.Time { return *now }
	config.ObservedIPPolicy.AllowDocumentation = true
	registry, err := NewPrivateRegistry(RegistryOptions{Config: config, ServerPeer: server, ServerIncarnation: 7, Signer: key,
		Resolver: fixedCountryResolver{country: "DE"}, ControlAddresses: []string{"/ip4/203.0.113.7/tcp/4000/p2p/" + server.String()},
	})
	privateMust(t, err)
	t.Cleanup(registry.Close)
	return registry, server
}

func TestPrivateRegistryIsolationAndReceipts(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	registry, server := privateRegistryFixture(t, &now)
	public, _ := testRegistry(t, &now, fixedCountryResolver{country: "DE"})
	defer public.Close()
	key, exit := testIdentity(t)
	locator := [32]byte{1}
	signed := signedPrivateExit(t, key, exit, now, 1)
	if _, err := VerifyDescriptor(signed, exit, now); err == nil {
		t.Fatal("private descriptor accepted as public Exit")
	}
	if _, err := public.Register(context.Background(), exit, RegistrationID{1}, ChannelToken{1}, signed, netip.MustParseAddr("198.51.100.10")); err == nil {
		t.Fatal("private Exit entered public registry")
	}
	publicRegistration, err := public.Register(context.Background(), exit, RegistrationID{1}, ChannelToken{1}, testSignedExit(t, key, exit, now, 1, 1, true), netip.MustParseAddr("198.51.100.10"))
	privateMust(t, err)
	if _, err := VerifyPrivateReceipt(publicRegistration.SignedReceipt.SignedEnvelope, public.serverPeer, locator, now); err == nil {
		t.Fatal("public receipt accepted in private domain")
	}
	verified, err := protocol.VerifySignedAnnouncementRecord(signed, exit, now)
	privateMust(t, err)
	mixed := proto.Clone(verified.Announcement).(*irviov1.NodeAnnouncement)
	mixed.Capabilities |= protocol.CapExit
	mixed.Country = "DE"
	if _, err := protocol.SealAnnouncement(key, mixed, now); err == nil {
		t.Fatal("ambiguous public/private capabilities accepted")
	}
	if _, err := registry.RegisterPrivate(context.Background(), exit, RegistrationID{1}, ChannelToken{1}, testSignedExit(t, key, exit, now, 1, 1, true), netip.MustParseAddr("198.51.100.10"), locator); err == nil {
		t.Fatal("public descriptor entered private registry")
	}
	registration, err := registry.RegisterPrivate(context.Background(), exit, RegistrationID{1}, ChannelToken{1}, signed, netip.MustParseAddr("198.51.100.10"), locator)
	privateMust(t, err)
	if len(registry.Countries()) != 0 || registry.Snapshot().Countries != 0 {
		t.Fatal("private country index exposed")
	}
	if _, err := registry.Sample("DE", 1, nil); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("private sampling allowed")
	}
	if _, err := VerifyReceipt(registration.SignedReceipt.SignedEnvelope, server, now); err == nil {
		t.Fatal("private receipt accepted in public domain")
	}
	if _, err := VerifyPrivateReceipt(registration.SignedReceipt.SignedEnvelope, server, [32]byte{2}, now); err == nil {
		t.Fatal("receipt not locator-bound")
	}
	if _, err := VerifyPrivateReceipt(registration.SignedReceipt.SignedEnvelope, server, locator, now.Add(6*time.Minute)); err == nil {
		t.Fatal("expired private receipt accepted")
	}
	candidate, err := registry.LookupPrivate(exit, locator)
	privateMust(t, err)
	if _, err := CandidateToProto(candidate); err == nil {
		t.Fatal("private candidate exported by public encoder")
	}
	wire, err := PrivateCandidateToProto(candidate)
	privateMust(t, err)
	if _, err := PrivateCandidateFromProto(wire, exit, locator, now); err != nil {
		t.Fatal(err)
	}
	if _, err := CandidateFromProto(wire, now); err == nil {
		t.Fatal("private candidate accepted by public decoder")
	}
	_, other := testIdentity(t)
	if _, err := PrivateCandidateFromProto(wire, other, locator, now); err == nil {
		t.Fatal("wrong target accepted")
	}
	forged := proto.Clone(wire).(*irviov1.BrokeredCandidate)
	forged.OwnerRoutes[0].ServerIncarnation++
	if _, err := PrivateCandidateFromProto(forged, exit, locator, now); err == nil {
		t.Fatal("forged owner incarnation accepted")
	}
	forged = proto.Clone(wire).(*irviov1.BrokeredCandidate)
	forged.OwnerReceipts[0][len(forged.OwnerReceipts[0])-1] ^= 1
	if _, err := PrivateCandidateFromProto(forged, exit, locator, now); err == nil {
		t.Fatal("tampered signature accepted")
	}
	if _, err := registry.LookupPrivate(exit, [32]byte{2}); !errors.Is(err, ErrNotFound) {
		t.Fatal("locator probing disclosed entry")
	}
	if len(registry.PrivateLocators()) != 1 {
		t.Fatal("live provider missing")
	}
	now = now.Add(registry.config.RegistrationStale)
	if _, err := registry.LookupPrivate(exit, locator); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale registration selected")
	}
	if len(registry.PrivateLocators()) != 0 {
		t.Fatal("stale locator still published")
	}
	_, err = registry.Heartbeat(registration.Owner, 1)
	privateMust(t, err)
	if _, err := registry.LookupPrivate(exit, locator); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateRegistryReplacementAndLocatorRotation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	registry, _ := privateRegistryFixture(t, &now)
	key, exit := testIdentity(t)
	old, err := registry.RegisterPrivate(context.Background(), exit, RegistrationID{1}, ChannelToken{1}, signedPrivateExit(t, key, exit, now, 1), netip.MustParseAddr("198.51.100.10"), [32]byte{1})
	privateMust(t, err)
	newer, err := registry.RegisterPrivate(context.Background(), exit, RegistrationID{2}, ChannelToken{2}, signedPrivateExit(t, key, exit, now, 2), netip.MustParseAddr("198.51.100.10"), [32]byte{2})
	privateMust(t, err)
	if registry.ChannelClosed(old.Owner) {
		t.Fatal("late close removed replacement")
	}
	if _, err := registry.Heartbeat(old.Owner, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("old owner heartbeat accepted")
	}
	if _, err := registry.LookupPrivate(exit, [32]byte{1}); !errors.Is(err, ErrNotFound) {
		t.Fatal("old locator survives rotation")
	}
	if _, err := registry.RegisterPrivate(context.Background(), exit, RegistrationID{3}, ChannelToken{3}, signedPrivateExit(t, key, exit, now, 1), netip.MustParseAddr("198.51.100.10"), [32]byte{1}); !errors.Is(err, ErrStale) {
		t.Fatal("descriptor rollback accepted")
	}
	privateMust(t, registry.Unregister(newer.Owner))
	if len(registry.PrivateLocators()) != 0 {
		t.Fatal("unregistered locator retained")
	}
}

func TestPrivateFrameBoundsAndVersion(t *testing.T) {
	var oversized [4]byte
	binary.BigEndian.PutUint32(oversized[:], PrivateMaxFrameSize+1)
	if _, err := readPrivateRequest(bytes.NewReader(oversized[:])); err == nil {
		t.Fatal("oversized frame accepted")
	}
	request := &irviov1.PrivateRendezvousRequest{ProtocolVersion: 2, Message: &irviov1.PrivateRendezvousRequest_Lookup{Lookup: &irviov1.PrivateRendezvousLookup{}}}
	var buffer bytes.Buffer
	privateMust(t, writeProtoFrameLimit(&buffer, request, PrivateMaxFrameSize))
	if _, err := readPrivateRequest(&buffer); err == nil {
		t.Fatal("unknown version accepted")
	}
}

func TestSignalingResponseFencesWrongOrReplacedChannel(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	registry, server := privateRegistryFixture(t, &now)
	_, exit := testIdentity(t)
	_, attacker := testIdentity(t)
	owner := Owner{ExitPeer: exit, ServerIncarnation: 7, RegistrationID: RegistrationID{1}, ChannelToken: ChannelToken{1}}
	s := &Service{registry: registry, pending: make(map[RequestID]pendingSignal), channels: map[peer.ID]*registrationChannel{exit: {owner: owner}}}
	id := RequestID{1}
	request := &irviov1.RendezvousSignal{ProtocolVersion: ProtocolVersion, RequestHandle: id[:], TargetPeerId: []byte(exit),
		OwnerRoute:        &irviov1.OwnerRoute{ServerPeerId: []byte(server), ServerIncarnation: 7, RegistrationId: owner.RegistrationID[:]},
		DeadlineUnixMilli: now.Add(time.Second).UnixMilli(), SignedMessage: []byte("opaque"),
	}
	responseChannel, ok := s.reservePending(id, owner, request)
	if !ok {
		t.Fatal("pending request not reserved")
	}
	response := proto.Clone(request).(*irviov1.RendezvousSignal)
	response.Response = true
	wrongOwner := owner
	wrongOwner.ExitPeer = attacker
	if s.deliverResponse(response, wrongOwner) {
		t.Fatal("another Exit answered request")
	}
	tampered := proto.Clone(response).(*irviov1.RendezvousSignal)
	tampered.DeadlineUnixMilli++
	if s.deliverResponse(tampered, owner) {
		t.Fatal("changed request binding accepted")
	}
	s.channels[exit].owner.ChannelToken = ChannelToken{2}
	if s.deliverResponse(response, owner) {
		t.Fatal("replaced channel answered request")
	}
	s.channels[exit].owner = owner
	if !s.deliverResponse(response, owner) || len(responseChannel) != 1 {
		t.Fatal("valid channel response rejected")
	}
}

func TestPrivateServiceRegistrationLookupAndNoPublicFallback(t *testing.T) {
	serverHost, serverKey := newRendezvousHost(t)
	exitHost, exitKey := newRendezvousHost(t)
	clientHost, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, exitHost, serverHost)
	connectRendezvousHosts(t, clientHost, serverHost)
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.HeartbeatInterval, config.RegistrationStale, config.RegistrationExpiry = 50*time.Millisecond, time.Second, 2*time.Second
	config.Admission.HeartbeatInterval = time.Millisecond
	config.Admission.GETInterval = time.Millisecond
	options := RegistryOptions{Config: config, ServerPeer: serverHost.ID(), ServerIncarnation: 5, Signer: serverKey,
		Resolver: fixedCountryResolver{}, ControlAddresses: []string{hostControlAddress(t, serverHost)}}
	publicRegistry, err := NewRegistry(options)
	privateMust(t, err)
	publicService, err := NewService(ServiceOptions{Host: serverHost, Registry: publicRegistry})
	privateMust(t, err)
	defer publicService.Close()
	signed := signedPrivateExit(t, exitKey, exitHost.ID(), time.Now(), 1)
	registrationOptions := RegistrationClientOptions{Host: exitHost, Server: serverHost.ID(), SignedDescriptor: signed, Config: config, Handler: &echoSignalHandler{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if wrong, err := OpenPrivateRegistration(ctx, registrationOptions, [32]byte{1}); err == nil {
		wrong.Close(ctx)
		t.Fatal("old server silently accepted private registration")
	}
	if publicRegistry.Snapshot().Registrations != 0 {
		t.Fatal("private fallback to public registration")
	}
	privateRegistry, err := NewPrivateRegistry(options)
	privateMust(t, err)
	privateService, err := NewPrivateService(ServiceOptions{Host: serverHost, Registry: privateRegistry})
	privateMust(t, err)
	defer privateService.Close()
	registration, err := OpenPrivateRegistration(ctx, registrationOptions, [32]byte{1})
	privateMust(t, err)
	defer registration.Close(ctx)
	client, err := NewClient(clientHost, time.Second)
	privateMust(t, err)
	candidate, err := client.LookupPrivate(ctx, serverHost.ID(), exitHost.ID(), [32]byte{1})
	privateMust(t, err)
	if candidate.Descriptor.Subject != exitHost.ID() {
		t.Fatal("wrong exact target")
	}
	countries, err := client.ListLocalAvailableCountries(ctx, serverHost.ID())
	privateMust(t, err)
	if len(countries) != 0 || publicRegistry.Snapshot().Registrations != 0 {
		t.Fatal("private registration leaked publicly")
	}
	if _, err := client.LookupPrivate(ctx, serverHost.ID(), exitHost.ID(), [32]byte{2}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong locator: %v", err)
	}
	request, err := NewRequestID(nil)
	privateMust(t, err)
	wire, err := PrivateCandidateToProto(candidate)
	privateMust(t, err)
	response, err := client.SignalPrivate(ctx, candidate.Routes[0], &irviov1.RendezvousSignal{ProtocolVersion: ProtocolVersion, RequestHandle: request[:], TargetPeerId: []byte(exitHost.ID()), OwnerRoute: wire.OwnerRoutes[0], SignedMessage: []byte("bounded opaque test signal"), DeadlineUnixMilli: time.Now().Add(time.Second).UnixMilli()})
	privateMust(t, err)
	if !bytes.HasPrefix(response.SignedMessage, []byte("response:")) {
		t.Fatal("private owner channel not routed")
	}
	privateMust(t, registration.Update(ctx, signedPrivateExit(t, exitKey, exitHost.ID(), time.Now(), 2)))
	candidate, err = client.LookupPrivate(ctx, serverHost.ID(), exitHost.ID(), [32]byte{1})
	privateMust(t, err)
	if candidate.Descriptor.Version.Sequence != 2 {
		t.Fatal("private descriptor update missing")
	}
	limit := time.Now().Add(time.Second)
	for registration.Snapshot().LastAcknowledgement.IsZero() && time.Now().Before(limit) {
		time.Sleep(10 * time.Millisecond)
	}
	if registration.Snapshot().LastAcknowledgement.IsZero() {
		t.Fatal("private heartbeat not acknowledged")
	}
	privateMust(t, registration.Close(ctx))
	for privateRegistry.Snapshot().Registrations != 0 && time.Now().Before(limit) {
		time.Sleep(10 * time.Millisecond)
	}
	if privateRegistry.Snapshot().Registrations != 0 {
		t.Fatal("private unregister leaked")
	}
	privateMust(t, privateService.Close())
	if _, err := client.ListLocalAvailableCountries(ctx, serverHost.ID()); err != nil {
		t.Fatalf("closing private service removed public handler: %v", err)
	}
}

func TestPrivateRegistrationCancellation(t *testing.T) {
	server, _ := newRendezvousHost(t)
	exit, key := newRendezvousHost(t)
	connectRendezvousHosts(t, exit, server)
	seen, ended := make(chan struct{}), make(chan struct{})
	server.SetStreamHandler(libprotocol.ID(PrivateProtocolID), func(stream network.Stream) {
		defer close(ended)
		defer stream.Close()
		if _, err := readPrivateRequest(stream); err != nil {
			return
		}
		close(seen)
		_, _ = stream.Read(make([]byte, 1))
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		registration, err := OpenPrivateRegistration(ctx, RegistrationClientOptions{Host: exit, Server: server.ID(), Config: DefaultConfig(),
			SignedDescriptor: signedPrivateExit(t, key, exit.ID(), time.Now(), 1), Handler: &echoSignalHandler{},
		}, [32]byte{1})
		if registration != nil {
			_ = registration.Close(context.Background())
		}
		result <- err
	}()
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("registration not started")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("registration ignored cancellation")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("cancelled stream leaked")
	}
}
