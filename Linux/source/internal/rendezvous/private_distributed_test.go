// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/reachability"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/proto"
)

func distributedPrivateNode(t *testing.T, bootstrap ...string) *discovery.Node {
	t.Helper()
	node, err := discovery.Start(context.Background(), discovery.Config{DataDir: filepath.Join(t.TempDir(), "profile"), ListenPort: 0, DHTMode: "server", Bootstrap: bootstrap, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	privateMust(t, err)
	t.Cleanup(func() { node.Close() })
	return node
}

func distributedPrivateProof(t *testing.T, subject peer.ID) reachability.Proof {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	var signed []*irviov1.SignedReachabilityAttestation
	for i := 0; i < 2; i++ {
		key, verifier := testIdentity(t)
		payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&irviov1.ReachabilityAttestation{
			ProtocolVersion: reachability.ProtocolVersion, SubjectPeerId: []byte(subject), VerifierPeerId: []byte(verifier), Family: irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4,
			ObservedIp: netip.MustParseAddr("203.0.113.7").AsSlice(), ExternalControlPort: 4000, NetworkGeneration: 1, PublicEpoch: 1,
			AttemptId: bytes.Repeat([]byte{byte(i + 1)}, reachability.AttemptIDSize), VerifiedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(time.Minute).Unix(),
		})
		privateMust(t, err)
		signature, err := key.Sign(append([]byte("hydra/reachability-attestation/1.0.0\x00"), payload...))
		privateMust(t, err)
		public, err := crypto.MarshalPublicKey(key.GetPublic())
		privateMust(t, err)
		signed = append(signed, &irviov1.SignedReachabilityAttestation{Attestation: payload, Signature: signature, VerifierPublicKey: public})
	}
	proof, err := reachability.BuildProof(subject, signed, reachability.DefaultProofPolicy())
	privateMust(t, err)
	return proof
}

func TestPrivateDistributedRegistrationLookupAndSignaling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	bootstrap := distributedPrivateNode(t)
	var address string
	for _, addr := range bootstrap.Host().Addrs() {
		if strings.HasPrefix(addr.String(), "/ip4/127.0.0.1/tcp/") {
			address = addr.String() + "/p2p/" + bootstrap.PeerID().String()
			break
		}
	}
	if address == "" {
		t.Fatal("bootstrap has no loopback address")
	}
	owners := []*discovery.Node{distributedPrivateNode(t, address), distributedPrivateNode(t, address)}
	exit, exitKey := newRendezvousHost(t)
	locator := [32]byte{27}
	signed := signedPrivateExit(t, exitKey, exit.ID(), time.Now(), 1)
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.Admission.GETInterval = time.Millisecond
	var registries []*Registry
	var registrations []*RegistrationClient
	var services []*Service
	published := make(chan peer.ID, 16)
	for _, owner := range owners {
		privateMust(t, owner.UpdatePublicReachability([]reachability.Proof{distributedPrivateProof(t, owner.PeerID())}))
		privateMust(t, owner.ActivateRendezvousCapability(ctx, nil))
		options := RegistryOptions{Config: config, ServerPeer: owner.PeerID(), ServerIncarnation: owner.LocalAnnouncement().Incarnation, Signer: owner.IdentityPrivateKey(), Resolver: fixedCountryResolver{}, ControlAddresses: []string{hostControlAddress(t, owner.Host())}}
		publicRegistry, err := NewRegistry(options)
		privateMust(t, err)
		publicService, err := NewService(ServiceOptions{Host: owner.Host(), Registry: publicRegistry})
		privateMust(t, err)
		t.Cleanup(func() { publicService.Close() })
		registry, err := NewPrivateRegistry(options)
		privateMust(t, err)
		service, err := NewPrivateService(ServiceOptions{Host: owner.Host(), Registry: registry, PrivatePublisher: privatePublisherFunc(func(ctx context.Context, key [32]byte) error {
			err := owner.ProvidePrivateOwner(ctx, key)
			if err == nil {
				select {
				case published <- owner.PeerID():
				default:
				}
			}
			return err
		})})
		privateMust(t, err)
		t.Cleanup(func() { service.Close() })
		services = append(services, service)
		registries = append(registries, registry)
		privateMust(t, owner.SetPrivateRendezvousReady(true))
		connectRendezvousHosts(t, exit, owner.Host())
		registration, err := OpenPrivateRegistration(ctx, RegistrationClientOptions{Host: exit, Server: owner.PeerID(), Config: config, SignedDescriptor: signed, Handler: &echoSignalHandler{}}, locator)
		privateMust(t, err)
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			registration.Close(cleanup)
		})
		registrations = append(registrations, registration)
	}
	seen := make(map[peer.ID]bool)
	for len(seen) < 2 {
		select {
		case id := <-published:
			seen[id] = true
		case <-ctx.Done():
			t.Fatal("live registrations were not published")
		}
	}
	clientNode := distributedPrivateNode(t, address)
	client, err := NewClient(clientNode.Host(), 2*time.Second)
	privateMust(t, err)
	lookup, err := NewPrivateLookup(clientNode, client, exit.ID(), locator)
	privateMust(t, err)
	result, err := lookup.Lookup(ctx)
	privateMust(t, err)
	if result.Candidate.Descriptor.Subject != exit.ID() || len(result.Candidate.Routes) != 2 {
		t.Fatalf("exact distributed lookup lost owners: %+v", result.Candidate.Routes)
	}
	for _, owner := range owners {
		countries, err := client.ListLocalAvailableCountries(ctx, owner.PeerID())
		privateMust(t, err)
		if len(countries) != 0 {
			t.Fatal("private registration appeared in public countries")
		}
	}
	privateMust(t, services[0].Close())
	// An authenticated eligible owner cannot replay another owner's otherwise valid receipt.
	foreign, err := registries[1].LookupPrivate(exit.ID(), locator)
	privateMust(t, err)
	foreignWire, err := PrivateCandidateToProto(foreign)
	privateMust(t, err)
	var forgedRequests atomic.Int32
	owners[0].Host().SetStreamHandler(libprotocol.ID(PrivateProtocolID), func(stream network.Stream) {
		defer stream.Close()
		_ = stream.SetDeadline(time.Now().Add(time.Second))
		request, err := readPrivateRequest(stream)
		if err != nil || request.GetLookup() == nil {
			_ = stream.Reset()
			return
		}
		response := &irviov1.PrivateRendezvousResponse{ProtocolVersion: ProtocolVersion,
			Message: &irviov1.PrivateRendezvousResponse_Lookup{Lookup: &irviov1.PrivateRendezvousLookupResult{
				RequestId: request.GetLookup().RequestId, Candidate: foreignWire,
			}}}
		forgedRequests.Add(1)
		_ = writeProtoFrameLimit(stream, response, PrivateMaxFrameSize)
	})
	defer owners[0].Host().RemoveStreamHandler(libprotocol.ID(PrivateProtocolID))
	if _, err := client.LookupPrivate(ctx, owners[0].PeerID(), exit.ID(), locator); !errors.Is(err, ErrWrongPeer) {
		t.Fatalf("authenticated owner replay was not rejected: %v", err)
	}
	result, err = lookup.Lookup(ctx)
	privateMust(t, err)
	if !result.Partial || len(result.Candidate.Routes) != 1 || result.Candidate.Routes[0].ServerPeer != owners[1].PeerID() || forgedRequests.Load() < 2 {
		t.Fatal("forged DHT owner reply contaminated or blocked the surviving authentic route")
	}
	owners[0].Host().RemoveStreamHandler(libprotocol.ID(PrivateProtocolID))
	result, err = lookup.Lookup(ctx)
	privateMust(t, err)
	if !result.Partial || len(result.Candidate.Routes) != 1 || result.Candidate.Routes[0].ServerPeer != owners[1].PeerID() {
		t.Fatal("failed owner blocked surviving route")
	}
	if _, err := NewBrokerCarrier(client, owners[1].PeerID(), result.Candidate); err == nil {
		t.Fatal("public carrier accepted private candidate")
	}
	if _, err := NewPrivateBrokerCarrier(client, clientNode.PeerID(), locator, result.Candidate); err == nil {
		t.Fatal("private carrier accepted wrong target")
	}
	if _, err := NewPrivateBrokerCarrier(client, exit.ID(), [32]byte{99}, result.Candidate); err == nil {
		t.Fatal("private carrier accepted wrong locator")
	}
	provider, err := NewPrivateCandidateProvider(clientNode.PeerID(), lookup, client)
	privateMust(t, err)
	batch, err := provider.GetCandidates(ctx, exitselector.CandidateRequest{
		Target: exitselector.Target{Kind: exitselector.TargetPrivatePeer, PeerID: exit.ID()}, Limit: 5,
	})
	privateMust(t, err)
	if len(batch.Candidates) != 1 || batch.Candidates[0].PeerID != exit.ID() || batch.Country != "" {
		t.Fatal("distributed private provider did not return exact country-free target")
	}
	carrier, err := provider.BrokerCarrier(batch.Candidates[0])
	privateMust(t, err)
	if _, err := carrier.Exchange(ctx, clientNode.PeerID(), []byte("wrong target")); err == nil {
		t.Fatal("private carrier changed target during exchange")
	}
	message, err := carrier.Exchange(ctx, exit.ID(), []byte("private carrier test"))
	privateMust(t, err)
	if !bytes.Equal(message, []byte("response:private carrier test")) {
		t.Fatalf("private carrier response: %q", message)
	}
	wire, err := PrivateCandidateToProto(result.Candidate)
	privateMust(t, err)
	request, err := NewRequestID(nil)
	privateMust(t, err)
	response, err := client.SignalPrivate(ctx, result.Candidate.Routes[0], &irviov1.RendezvousSignal{ProtocolVersion: ProtocolVersion, RequestHandle: request[:], TargetPeerId: []byte(exit.ID()), OwnerRoute: wire.OwnerRoutes[0], SignedMessage: []byte("distributed private test"), DeadlineUnixMilli: time.Now().Add(time.Second).UnixMilli()})
	privateMust(t, err)
	if !bytes.HasPrefix(response.SignedMessage, []byte("response:")) {
		t.Fatal("signal did not reach selected Exit")
	}
	privateMust(t, registrations[1].Close(ctx))
	for registries[1].Snapshot().Registrations != 0 {
		select {
		case <-ctx.Done():
			t.Fatal("unregister not drained")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err := lookup.Lookup(ctx); !errors.Is(err, ErrPrivateLookupIncomplete) {
		t.Fatalf("stale DHT hints produced live candidate: %v", err)
	}
	replacement, err := OpenPrivateRegistration(ctx, RegistrationClientOptions{Host: exit, Server: owners[1].PeerID(), Config: config, SignedDescriptor: signed, Handler: &echoSignalHandler{}}, locator)
	privateMust(t, err)
	defer replacement.Close(ctx)
	newResult, err := lookup.Lookup(ctx)
	privateMust(t, err)
	if len(newResult.Candidate.Routes) != 1 || newResult.Candidate.Routes[0].RegistrationID == result.Candidate.Routes[0].RegistrationID {
		t.Fatal("new registration retained stale owner route")
	}
	request, err = NewRequestID(nil)
	privateMust(t, err)
	_, err = client.SignalPrivate(ctx, result.Candidate.Routes[0], &irviov1.RendezvousSignal{ProtocolVersion: ProtocolVersion, RequestHandle: request[:], TargetPeerId: []byte(exit.ID()), OwnerRoute: wire.OwnerRoutes[0], SignedMessage: []byte("stale route must fail"), DeadlineUnixMilli: time.Now().Add(time.Second).UnixMilli()})
	if err == nil {
		t.Fatal("replaced registration accepted stale signaling route")
	}
	if _, err := carrier.Exchange(ctx, exit.ID(), []byte("replaced carrier route")); err == nil {
		t.Fatal("private carrier reused replaced registration")
	}
	if messages, _ := carrier.Usage(); messages != 3 {
		t.Fatalf("private carrier message budget bypassed: %d", messages)
	}
}
