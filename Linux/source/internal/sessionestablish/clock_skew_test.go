// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessionestablish

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitaccess"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPrivateEstablishmentClockSkew(t *testing.T) {
	for _, offset := range []time.Duration{-2 * time.Second, 0, 2 * time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			now := time.Now().Truncate(time.Millisecond)
			clientNow := now.Add(offset)
			clientKey, client := transcriptIdentity(t)
			exitKey, exit := transcriptIdentity(t)
			base := signedDescriptorBinding(t, exitKey, exit, now)
			announcement, err := protocol.VerifySignedAnnouncement(base.SignedEnvelope, exit, now)
			if err != nil {
				t.Fatal(err)
			}
			announcement.Capabilities = protocol.CapDiscovery | protocol.CapPrivateExit
			announcement.Country = ""
			announcement.SessionFeatures = append(announcement.SessionFeatures, protocol.PrivateExitFeature)
			signed, err := protocol.SealAnnouncement(exitKey, announcement, now)
			if err != nil {
				t.Fatal(err)
			}
			descriptor, err := BindingFromSignedAnnouncement(signed, exit, now)
			if err != nil {
				t.Fatal(err)
			}
			processor := &testOfferProcessor{now: now, exit: exit}
			responder, err := NewResponder(ResponderOptions{
				Local: exit, PrivateKey: exitKey, PrivateMode: true,
				Descriptor:        func() DescriptorBinding { return descriptor },
				NetworkGeneration: func() uint64 { return 12 }, Authorizer: AllowCompatiblePeer{},
				Processor: processor, Now: func() time.Time { return now },
				Admission: exitaccess.PolicyFunc(func(ctx context.Context, id peer.ID) (exitaccess.Permit, error) {
					if id != client {
						return exitaccess.Permit{}, exitaccess.ErrDenied
					}
					return exitaccess.Permit{Revision: 7, Context: ctx, Release: func() {}}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer responder.Close()
			clientDescriptor := signedDescriptorBinding(t, clientKey, client, clientNow)
			initiator, err := NewInitiator(InitiatorOptions{
				Local: client, PrivateKey: clientKey, LocalAWGKeyEpoch: 2,
				SignedAnnouncement: clientDescriptor.SignedEnvelope, NetworkGeneration: 11,
				RuntimeGeneration: 13, AWGProfileHash: [HashSize]byte{1}, Now: func() time.Time { return clientNow },
			})
			if err != nil {
				t.Fatal(err)
			}
			factory := OfferFactoryFunc(func(_ context.Context, ready ReadyContext) (*irviov1.IceOffer, error) {
				responder.mu.Lock()
				pending := responder.pending[ready.Attempt]
				valid := pending != nil && !pending.expires.After(now.Add(DefaultPolicy().ReadyLifetime))
				responder.mu.Unlock()
				if !valid {
					t.Fatal("skew extended local READY ownership")
				}
				return testWireOffer(client, exit, clientNow), nil
			})
			result, err := initiator.EstablishPrivate(context.Background(), responderCarrier{responder: responder}, exit, descriptor, factory, true, false)
			if err != nil {
				t.Fatal(err)
			}
			if processor.calls.Load() != 1 || processor.admissionRevision.Load() != 7 {
				t.Fatal("lost admission or duplicate activation")
			}
			if _, err := responder.HandleSignal(context.Background(), "", result.Offer.Wire); !errors.Is(err, ErrExpired) || processor.calls.Load() != 1 {
				t.Fatal("duplicate OFFER escaped replay guard", err)
			}
			expiredFactory := OfferFactoryFunc(func(_ context.Context, ready ReadyContext) (*irviov1.IceOffer, error) {
				responder.mu.Lock()
				responder.pending[ready.Attempt].expires = now.Add(-time.Millisecond)
				responder.mu.Unlock()
				return testWireOffer(client, exit, clientNow), nil
			})
			if _, err := initiator.EstablishPrivate(context.Background(), responderCarrier{responder: responder}, exit, descriptor, expiredFactory, true, false); !errors.Is(err, ErrExpired) || processor.calls.Load() != 1 {
				t.Fatal("clock grace extended expired local challenge", err)
			}
			if _, err := Verify(result.Ready.Prepare.Wire, client, exit, now.Add(30*time.Second), DefaultPolicy()); !errors.Is(err, ErrExpired) {
				t.Fatalf("stale signed PREPARE accepted: %v", err)
			}
		})
	}
}

func TestRemoteDeadlineRemainsBounded(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	key, client := transcriptIdentity(t)
	_, exit := transcriptIdentity(t)
	policy := DefaultPolicy()
	for _, offset := range []time.Duration{-2 * time.Second, 0, 2 * time.Second} {
		payload := preparePayload(client, exit, now.Add(offset), 1)
		wire, err := Seal(key, payload, now.Add(offset), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(wire, client, exit, now, policy); err != nil {
			t.Fatal(offset, err)
		}
		deadline := remoteDeadline(payload, now, policy)
		if !deadline.After(now) || deadline.After(now.Add(time.Second)) {
			t.Fatal("remote TTL extended", offset, deadline)
		}
		if _, err := Verify(wire, client, exit, now.Add(offset+6*time.Second), policy); !errors.Is(err, ErrExpired) {
			t.Fatal("expiry plus skew boundary accepted", err)
		}
		strict := policy
		strict.ClockSkew = 0
		if offset != 0 {
			if _, err := Verify(wire, client, exit, now, strict); err == nil {
				t.Fatal("zero skew policy ignored")
			}
		}
	}
	future := preparePayload(client, exit, now.Add(6*time.Second), 1)
	wire, err := Seal(key, future, now.Add(6*time.Second), policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(wire, client, exit, now, policy); !errors.Is(err, ErrInvalid) {
		t.Fatal("future bound ignored", err)
	}
}
