// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessionestablish

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/exitaccess"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type testOfferProcessor struct {
	calls             atomic.Uint64
	admissionRevision atomic.Uint64
	now               time.Time
	exit              peer.ID
}

func (p *testOfferProcessor) ProcessOffer(_ context.Context, end EndPeerContext, offer *irviov1.SessionOffer) (AnswerResult, error) {
	p.calls.Add(1)
	p.admissionRevision.Store(end.AdmissionRevision)
	answer := &irviov1.IceAnswer{
		ProtocolVersion: 1, SessionId: append([]byte(nil), offer.IceOffer.SessionId...),
		InitiatorPeerId: []byte(end.RemotePeer), ResponderPeerId: []byte(p.exit),
		Purpose: irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT,
		Role:    irviov1.IceRole_ICE_ROLE_CONTROLLED,
		Description: &irviov1.IceDescription{
			Credentials: &irviov1.IceCredentials{Ufrag: "exitUfrag", Pwd: "1234567890123456789012"},
			Candidates: []*irviov1.IceCandidate{{
				Type:    irviov1.IceCandidateType_ICE_CANDIDATE_TYPE_HOST,
				Network: irviov1.IceNetworkType_ICE_NETWORK_TYPE_UDP4,
				Address: []byte{10, 0, 0, 2}, Port: 50002, Foundation: "exit",
				Priority: 100, Component: 1,
			}},
		},
		CreatedAtUnix: p.now.Unix(), ExpiresAtUnix: p.now.Add(time.Minute).Unix(),
	}
	return AnswerResult{
		ICEAnswer: answer,
		ProvisionalGrant: &irviov1.TunnelLease{
			LeaseId: bytes.Repeat([]byte{0x61}, 16), ExitPeerId: []byte(p.exit),
			ClientPeerId: []byte(end.RemotePeer), Ipv4Address: []byte{10, 88, 0, 2},
			Ipv4PrefixBits: 32, ValidUntilUnix: p.now.Add(5 * time.Minute).Unix(),
			Generation: 1, SessionToken: append([]byte(nil), end.OwnerToken[:16]...),
		},
		ExitControlAddresses: [][]byte{{10, 88, 0, 1}}, ExitControlPort: 41001,
		AWGProfileHash: [HashSize]byte{1}, ActivationDeadline: p.now.Add(10 * time.Second),
	}, nil
}

type responderCarrier struct {
	responder *Responder
	broker    peer.ID
}

func (c responderCarrier) Exchange(ctx context.Context, _ peer.ID, request []byte) ([]byte, error) {
	return c.responder.HandleSignal(ctx, c.broker, request)
}

func TestResponderAdmissionBindsEndClientAndDropsRevokedPrepare(t *testing.T) {
	for _, scenario := range []string{"allowed", "denied", "revoked-after-prepare"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().Truncate(time.Millisecond)
			clientKey, client := transcriptIdentity(t)
			exitKey, exit := transcriptIdentity(t)
			_, broker := transcriptIdentity(t)
			descriptor := signedDescriptorBinding(t, exitKey, exit, now)
			clientDescriptor := signedDescriptorBinding(t, clientKey, client, now)
			processor := &testOfferProcessor{now: now, exit: exit}
			responder, err := NewResponder(ResponderOptions{
				Local: exit, PrivateKey: exitKey, Descriptor: func() DescriptorBinding { return descriptor },
				NetworkGeneration: func() uint64 { return 12 }, Authorizer: AllowCompatiblePeer{},
				Admission: exitaccess.PolicyFunc(func(_ context.Context, authenticated peer.ID) (exitaccess.Permit, error) {
					if authenticated != client || authenticated == broker {
						t.Fatal("broker substituted for authenticated client")
					}
					if scenario == "denied" {
						return exitaccess.Permit{}, errors.New("denied")
					}
					return exitaccess.Permit{Revision: 7, Context: context.Background(), Release: func() {}}, nil
				}),
				Processor: processor, Policy: DefaultPolicy(), Now: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer responder.Close()
			initiator, err := NewInitiator(InitiatorOptions{
				Local: client, PrivateKey: clientKey, LocalAWGKeyEpoch: 2, SignedAnnouncement: clientDescriptor.SignedEnvelope,
				NetworkGeneration: 11, RuntimeGeneration: 13, AWGProfileHash: [HashSize]byte{1}, Policy: DefaultPolicy(), Now: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = initiator.Establish(context.Background(), responderCarrier{responder: responder, broker: broker}, exit, "DE", descriptor,
				OfferFactoryFunc(func(context.Context, ReadyContext) (*irviov1.IceOffer, error) {
					if scenario == "revoked-after-prepare" {
						responder.RevokePeer(client)
					}
					return testWireOffer(client, exit, now), nil
				}), true, false)
			if scenario == "allowed" {
				if err != nil || processor.calls.Load() != 1 || processor.admissionRevision.Load() != 7 {
					t.Fatalf("admission not bound: %v", err)
				}
			} else if err == nil || processor.calls.Load() != 0 || responder.Pending() != 0 {
				t.Fatal("denied/revoked request reached resource processor")
			}
		})
	}
}

func signedDescriptorBinding(t *testing.T, key crypto.PrivKey, id peer.ID, now time.Time) DescriptorBinding {
	t.Helper()
	announcement := &irviov1.NodeAnnouncement{
		ProtocolVersion: protocol.ProtocolVersion, NodeId: []byte(id),
		SessionEpoch: bytes.Repeat([]byte{7}, protocol.SessionEpochSize),
		Incarnation:  3, Sequence: 4, Capabilities: protocol.CapDiscovery | protocol.CapExit,
		Country: "DE", EgressIpv4: true, AwgPublicKey: bytes.Repeat([]byte{8}, 32),
		AwgKeyEpoch: 2, CreatedAtUnix: now.Add(-time.Minute).Unix(),
		ExpiresAtUnix: now.Add(time.Hour).Unix(), AcceptingNewSessions: true,
		SessionFeatures: []string{"session-establish-v1", "session-control-v1"},
	}
	signed, err := protocol.SealAnnouncement(key, announcement, now)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := BindingFromSignedAnnouncement(signed, id, now)
	if err != nil {
		t.Fatal(err)
	}
	return verified
}

func testWireOffer(client, exit peer.ID, now time.Time) *irviov1.IceOffer {
	return &irviov1.IceOffer{
		ProtocolVersion: 1, SessionId: bytes.Repeat([]byte{9}, 16),
		InitiatorPeerId: []byte(client), ResponderPeerId: []byte(exit),
		Purpose: irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT,
		Role:    irviov1.IceRole_ICE_ROLE_CONTROLLING,
		Description: &irviov1.IceDescription{
			Credentials: &irviov1.IceCredentials{Ufrag: "clientUfrag", Pwd: "1234567890123456789012"},
			Candidates: []*irviov1.IceCandidate{{
				Type:    irviov1.IceCandidateType_ICE_CANDIDATE_TYPE_HOST,
				Network: irviov1.IceNetworkType_ICE_NETWORK_TYPE_UDP4,
				Address: []byte{10, 0, 0, 1}, Port: 50001, Foundation: "client",
				Priority: 100, Component: 1,
			}},
		},
		CreatedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(time.Minute).Unix(),
	}
}

func TestInitiatorResponderSignedEstablishment(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	clientKey, client := transcriptIdentity(t)
	exitKey, exit := transcriptIdentity(t)
	clientDescriptor := signedDescriptorBinding(t, clientKey, client, now)
	descriptor := signedDescriptorBinding(t, exitKey, exit, now)
	processor := &testOfferProcessor{now: now, exit: exit}
	responder, err := NewResponder(ResponderOptions{
		Local: exit, PrivateKey: exitKey, Descriptor: func() DescriptorBinding { return descriptor },
		NetworkGeneration: func() uint64 { return 12 }, Authorizer: AllowCompatiblePeer{},
		Processor: processor, Policy: DefaultPolicy(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer responder.Close()
	initiator, err := NewInitiator(InitiatorOptions{
		Local: client, PrivateKey: clientKey, LocalAWGKeyEpoch: 2,
		SignedAnnouncement: clientDescriptor.SignedEnvelope,
		NetworkGeneration:  11, RuntimeGeneration: 13,
		AWGProfileHash: [HashSize]byte{1}, Policy: DefaultPolicy(),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := initiator.Establish(
		context.Background(), responderCarrier{responder: responder}, exit, "DE", descriptor,
		OfferFactoryFunc(func(context.Context, ReadyContext) (*irviov1.IceOffer, error) {
			return testWireOffer(client, exit, now), nil
		}), true, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Body.ProvisionalGrant == nil || processor.calls.Load() != 1 ||
		result.Ready.ExitNetworkGeneration != 12 {
		t.Fatalf("result=%+v processor calls=%d", result.Body, processor.calls.Load())
	}
}

func TestResponderAcceptsOlderDescriptorAsLocator(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	clientKey, client := transcriptIdentity(t)
	exitKey, exit := transcriptIdentity(t)
	clientDescriptor := signedDescriptorBinding(t, clientKey, client, now)
	current := signedDescriptorBinding(t, exitKey, exit, now)
	locator := current
	locator.Sequence--
	locator.ID[0] ^= 0xff
	processor := &testOfferProcessor{now: now, exit: exit}
	responder, err := NewResponder(ResponderOptions{
		Local: exit, PrivateKey: exitKey, Descriptor: func() DescriptorBinding { return current },
		NetworkGeneration: func() uint64 { return 12 }, Authorizer: AllowCompatiblePeer{},
		Processor: processor, Policy: DefaultPolicy(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer responder.Close()
	initiator, err := NewInitiator(InitiatorOptions{
		Local: client, PrivateKey: clientKey, LocalAWGKeyEpoch: 2,
		SignedAnnouncement: clientDescriptor.SignedEnvelope,
		NetworkGeneration:  11, RuntimeGeneration: 13, AWGProfileHash: [HashSize]byte{1},
		Policy: DefaultPolicy(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := initiator.Establish(
		context.Background(), responderCarrier{responder: responder}, exit, "DE", locator,
		OfferFactoryFunc(func(context.Context, ReadyContext) (*irviov1.IceOffer, error) {
			return testWireOffer(client, exit, now), nil
		}), true, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	readyDescriptor, err := BindingFromSignedAnnouncement(
		result.Ready.Ready.Payload.GetReady().SignedAnnouncement, exit, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if readyDescriptor.ID != current.ID || processor.calls.Load() != 1 {
		t.Fatalf("READY did not replace the locator with current descriptor: ready=%x current=%x calls=%d",
			readyDescriptor.ID, current.ID, processor.calls.Load())
	}
}

func TestResponderRejectsConflictingCurrentDescriptorLocator(t *testing.T) {
	current := DescriptorBinding{ID: [HashSize]byte{1}, Incarnation: 3, Sequence: 4}
	body := &irviov1.SessionPrepare{
		DescriptorId: make([]byte, HashSize), DescriptorIncarnation: 3, DescriptorSequence: 4,
	}
	if descriptorLocatorAccepted(body, current) {
		t.Fatal("same descriptor version with conflicting payload hash was accepted")
	}
	body.DescriptorId[0] = 1
	if !descriptorLocatorAccepted(body, current) {
		t.Fatal("identical descriptor version and hash was rejected")
	}
	body.DescriptorSequence = 5
	if descriptorLocatorAccepted(body, current) {
		t.Fatal("future descriptor sequence was accepted")
	}
}
