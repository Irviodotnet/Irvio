// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessionestablish

import (
	"context"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitaccess"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
)

func TestPrivateEstablishmentBindsTargetVersionAndAdmission(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	clientKey, client := transcriptIdentity(t)
	exitKey, exit := transcriptIdentity(t)
	_, broker := transcriptIdentity(t)
	clientDescriptor := signedDescriptorBinding(t, clientKey, client, now)
	publicDescriptor := signedDescriptorBinding(t, exitKey, exit, now)
	announcement, err := protocol.VerifySignedAnnouncement(publicDescriptor.SignedEnvelope, exit, now)
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
	options := ResponderOptions{Local: exit, PrivateKey: exitKey, PrivateMode: true, Descriptor: func() DescriptorBinding { return descriptor }, NetworkGeneration: func() uint64 { return 12 }, Authorizer: AllowCompatiblePeer{}, Processor: processor, Now: func() time.Time { return now }}
	if _, err := NewResponder(options); err == nil {
		t.Fatal("private responder accepted missing ACL")
	}
	options.Admission = exitaccess.PolicyFunc(func(ctx context.Context, id peer.ID) (exitaccess.Permit, error) {
		if id != client || id == broker {
			return exitaccess.Permit{}, exitaccess.ErrDenied
		}
		return exitaccess.Permit{Revision: 7, Context: ctx, Release: func() {}}, nil
	})
	responder, err := NewResponder(options)
	if err != nil {
		t.Fatal(err)
	}
	defer responder.Close()
	initiator, err := NewInitiator(InitiatorOptions{Local: client, PrivateKey: clientKey, LocalAWGKeyEpoch: 2, SignedAnnouncement: clientDescriptor.SignedEnvelope, NetworkGeneration: 11, RuntimeGeneration: 13, AWGProfileHash: [HashSize]byte{1}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	factory := OfferFactoryFunc(func(context.Context, ReadyContext) (*irviov1.IceOffer, error) {
		return testWireOffer(client, exit, now), nil
	})
	result, err := initiator.EstablishPrivate(context.Background(), responderCarrier{responder: responder, broker: broker}, exit, descriptor, factory, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready.Prepare.Payload.GetPrepare().CountryIntent != "" || string(result.Ready.Prepare.Payload.GetPrepare().PrivateTargetPeerId) != string(exit) || result.Answer.Payload.ProtocolVersion != PrivateProtocolVersion || processor.admissionRevision.Load() != 7 {
		t.Fatal("private target or ACL revision lost")
	}
	if _, err := initiator.Establish(context.Background(), responderCarrier{responder: responder}, exit, "DE", descriptor, factory, true, false); err == nil {
		t.Fatal("public request entered private responder")
	}
	options.PrivateMode = false
	publicResponder, err := NewResponder(options)
	if err != nil {
		t.Fatal(err)
	}
	defer publicResponder.Close()
	if _, err := initiator.EstablishPrivate(context.Background(), responderCarrier{responder: publicResponder}, exit, descriptor, factory, true, false); err == nil {
		t.Fatal("private request downgraded into public responder")
	}
	if _, err := initiator.EstablishPrivate(context.Background(), responderCarrier{responder: responder}, exit, publicDescriptor, factory, true, false); err == nil {
		t.Fatal("public descriptor accepted as private")
	}
	forgedBinding := descriptor
	forgedBinding.AWGKeyEpoch++
	if _, err := initiator.EstablishPrivate(context.Background(), responderCarrier{responder: responder}, exit, forgedBinding, factory, true, false); err == nil {
		t.Fatal("private metadata disagrees with its signed descriptor")
	}
	downgraded := result.Ready.Ready
	downgraded.Payload = proto.Clone(downgraded.Payload).(*irviov1.SessionEstablishPayload)
	downgraded.Payload.ProtocolVersion = ProtocolVersion
	if err := RequirePredecessor(downgraded, result.Ready.Prepare); err == nil {
		t.Fatal("transcript chain accepted protocol downgrade")
	}
}

func TestPrivatePrepareRejectsAmbiguousOrSubstitutedTarget(t *testing.T) {
	now := time.Now()
	key, client := transcriptIdentity(t)
	_, exit := transcriptIdentity(t)
	for _, mode := range []string{"country", "wrong-peer", "public-with-target"} {
		payload := preparePayload(client, exit, now, 1)
		payload.ProtocolVersion = PrivateProtocolVersion
		payload.GetPrepare().CountryIntent = ""
		payload.GetPrepare().PrivateTargetPeerId = []byte(exit)
		switch mode {
		case "country":
			payload.GetPrepare().CountryIntent = "DE"
		case "wrong-peer":
			payload.GetPrepare().PrivateTargetPeerId = []byte(client)
		case "public-with-target":
			payload.ProtocolVersion = ProtocolVersion
			payload.GetPrepare().CountryIntent = "DE"
		}
		if _, err := Seal(key, payload, now, DefaultPolicy()); err == nil {
			t.Fatalf("ambiguous target signed: %s", mode)
		}
	}
}
