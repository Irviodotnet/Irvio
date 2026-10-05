// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessionestablish

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

type Carrier interface {
	Exchange(context.Context, peer.ID, []byte) ([]byte, error)
}

type OfferFactory interface {
	BuildOffer(context.Context, ReadyContext) (*irviov1.IceOffer, error)
}

type OfferFactoryFunc func(context.Context, ReadyContext) (*irviov1.IceOffer, error)

func (f OfferFactoryFunc) BuildOffer(ctx context.Context, ready ReadyContext) (*irviov1.IceOffer, error) {
	return f(ctx, ready)
}

type InitiatorOptions struct {
	Local              peer.ID
	PrivateKey         crypto.PrivKey
	LocalAWGKeyEpoch   uint64
	SignedAnnouncement []byte
	NetworkGeneration  uint64
	RuntimeGeneration  uint64
	AWGProfileHash     [HashSize]byte
	Policy             Policy
	Now                func() time.Time
	Random             io.Reader
}

type Initiator struct {
	options InitiatorOptions
}

type ReadyContext struct {
	Attempt               AttemptID
	OwnerToken            [OwnerTokenSize]byte
	Exit                  peer.ID
	ExitNetworkGeneration uint64
	ExitAWGKeyEpoch       uint64
	ExitAWGPublicKey      [32]byte
	Challenge             [ChallengeSize]byte
	Prepare               Verified
	Ready                 Verified
}

type EstablishResult struct {
	Ready  ReadyContext
	Offer  Verified
	Answer Verified
	Body   *irviov1.SessionAnswer
}

func NewInitiator(options InitiatorOptions) (*Initiator, error) {
	if options.Local == "" || options.PrivateKey == nil || options.LocalAWGKeyEpoch == 0 ||
		len(options.SignedAnnouncement) == 0 ||
		options.NetworkGeneration == 0 || options.RuntimeGeneration == 0 ||
		options.AWGProfileHash == ([HashSize]byte{}) {
		return nil, ErrInvalid
	}
	signer, err := peer.IDFromPrivateKey(options.PrivateKey)
	if err != nil || signer != options.Local {
		return nil, ErrWrongPeer
	}
	if options.Policy.MaxLifetime == 0 {
		options.Policy = DefaultPolicy()
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	binding, err := BindingFromSignedAnnouncement(options.SignedAnnouncement, options.Local, options.Now())
	if err != nil || binding.AWGKeyEpoch != options.LocalAWGKeyEpoch {
		return nil, ErrConflict
	}
	options.SignedAnnouncement = append([]byte(nil), options.SignedAnnouncement...)
	return &Initiator{options: options}, nil
}

func (i *Initiator) Establish(
	ctx context.Context,
	carrier Carrier,
	exit peer.ID,
	country string,
	descriptor DescriptorBinding,
	factory OfferFactory,
	requestIPv4, requestIPv6 bool,
) (EstablishResult, error) {
	if ctx == nil || carrier == nil || exit == "" || exit == i.options.Local ||
		!validCountry(country) || descriptor.ID == ([HashSize]byte{}) ||
		descriptor.Incarnation == 0 || descriptor.Sequence == 0 ||
		descriptor.AWGKeyEpoch == 0 || factory == nil || (!requestIPv4 && !requestIPv6) {
		return EstablishResult{}, ErrInvalid
	}
	attempt, err := NewAttemptID(i.options.Random)
	if err != nil {
		return EstablishResult{}, err
	}
	var owner [OwnerTokenSize]byte
	if _, err := io.ReadFull(i.options.Random, owner[:]); err != nil {
		return EstablishResult{}, err
	}
	if owner == ([OwnerTokenSize]byte{}) {
		owner[len(owner)-1] = 1
	}
	now := i.options.Now()
	preparePayload := &irviov1.SessionEstablishPayload{
		ProtocolVersion: ProtocolVersion,
		Kind:            irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_PREPARE,
		SenderPeerId:    []byte(i.options.Local), RecipientPeerId: []byte(exit),
		AttemptId: append([]byte(nil), attempt[:]...), OwnerToken: append([]byte(nil), owner[:]...),
		CreatedAtUnixMilli: now.UnixMilli(),
		ExpiresAtUnixMilli: now.Add(i.options.Policy.PrepareLifetime).UnixMilli(),
		Body: &irviov1.SessionEstablishPayload_Prepare{Prepare: &irviov1.SessionPrepare{
			CountryIntent: country, DescriptorId: append([]byte(nil), descriptor.ID[:]...),
			DescriptorIncarnation: descriptor.Incarnation, DescriptorSequence: descriptor.Sequence,
			Purpose:           irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT,
			NetworkGeneration: i.options.NetworkGeneration, RuntimeGeneration: i.options.RuntimeGeneration,
		}},
	}
	prepareWire, err := Seal(i.options.PrivateKey, preparePayload, now, i.options.Policy)
	if err != nil {
		return EstablishResult{}, err
	}
	prepare, err := Verify(prepareWire, i.options.Local, exit, now, i.options.Policy)
	if err != nil {
		return EstablishResult{}, err
	}
	readyWire, err := carrier.Exchange(ctx, exit, prepareWire)
	if err != nil {
		return EstablishResult{}, err
	}
	ready, err := Verify(readyWire, exit, i.options.Local, i.options.Now(), i.options.Policy)
	if err != nil {
		return EstablishResult{}, err
	}
	if ready.Payload.Kind == irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_DECLINE {
		return EstablishResult{}, fmt.Errorf("%w: PREPARE declined", ErrCapacity)
	}
	if ready.Payload.Kind != irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_READY ||
		RequirePredecessor(ready, prepare) != nil {
		return EstablishResult{}, ErrChain
	}
	readyBody := ready.Payload.GetReady()
	verifiedDescriptor, err := BindingFromSignedAnnouncement(readyBody.SignedAnnouncement, exit, i.options.Now())
	if err != nil {
		return EstablishResult{}, err
	}
	if !descriptorRefreshAccepted(descriptor, verifiedDescriptor) ||
		readyBody.AwgKeyEpoch != verifiedDescriptor.AWGKeyEpoch {
		return EstablishResult{}, ErrConflict
	}
	var challenge [ChallengeSize]byte
	copy(challenge[:], readyBody.ExitChallenge)
	readyContext := ReadyContext{
		Attempt: attempt, OwnerToken: owner, Exit: exit,
		ExitNetworkGeneration: readyBody.NetworkGeneration,
		ExitAWGKeyEpoch:       readyBody.AwgKeyEpoch, ExitAWGPublicKey: verifiedDescriptor.AWGPublicKey,
		Challenge: challenge,
		Prepare:   prepare, Ready: ready,
	}
	iceOffer, err := factory.BuildOffer(ctx, readyContext)
	if err != nil {
		return EstablishResult{}, err
	}
	now = i.options.Now()
	offerPayload := &irviov1.SessionEstablishPayload{
		ProtocolVersion: ProtocolVersion,
		Kind:            irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_OFFER,
		SenderPeerId:    []byte(i.options.Local), RecipientPeerId: []byte(exit),
		AttemptId: append([]byte(nil), attempt[:]...), OwnerToken: append([]byte(nil), owner[:]...),
		PredecessorHash:    append([]byte(nil), ready.Hash[:]...),
		CreatedAtUnixMilli: now.UnixMilli(),
		ExpiresAtUnixMilli: minTime(now.Add(i.options.Policy.MaxLifetime), time.UnixMilli(ready.Payload.ExpiresAtUnixMilli)).UnixMilli(),
		Body: &irviov1.SessionEstablishPayload_Offer{Offer: &irviov1.SessionOffer{
			ReadyHash:     append([]byte(nil), ready.Hash[:]...),
			ExitChallenge: append([]byte(nil), challenge[:]...), IceOffer: iceOffer,
			ClientAwgKeyEpoch: i.options.LocalAWGKeyEpoch,
			ExitAwgKeyEpoch:   readyBody.AwgKeyEpoch,
			AwgProfileHash:    append([]byte(nil), i.options.AWGProfileHash[:]...),
			RequestIpv4:       requestIPv4, RequestIpv6: requestIPv6,
			ClientSignedAnnouncement: append([]byte(nil), i.options.SignedAnnouncement...),
		}},
	}
	offerWire, err := Seal(i.options.PrivateKey, offerPayload, now, i.options.Policy)
	if err != nil {
		return EstablishResult{}, err
	}
	offer, err := Verify(offerWire, i.options.Local, exit, now, i.options.Policy)
	if err != nil {
		return EstablishResult{}, err
	}
	answerWire, err := carrier.Exchange(ctx, exit, offerWire)
	if err != nil {
		return EstablishResult{}, err
	}
	answer, err := Verify(answerWire, exit, i.options.Local, i.options.Now(), i.options.Policy)
	if err != nil {
		return EstablishResult{}, err
	}
	if answer.Payload.Kind == irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_DECLINE {
		return EstablishResult{}, fmt.Errorf("%w: OFFER declined", ErrCapacity)
	}
	if answer.Payload.Kind != irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_ANSWER ||
		RequirePredecessor(answer, offer) != nil ||
		!bytes.Equal(answer.Payload.GetAnswer().AwgProfileHash, i.options.AWGProfileHash[:]) {
		return EstablishResult{}, ErrChain
	}
	return EstablishResult{Ready: readyContext, Offer: offer, Answer: answer, Body: answer.Payload.GetAnswer()}, nil
}

func descriptorRefreshAccepted(locator, current DescriptorBinding) bool {
	switch {
	case current.Incarnation < locator.Incarnation:
		return false
	case current.Incarnation > locator.Incarnation:
		return true
	case current.Sequence < locator.Sequence:
		return false
	case current.Sequence > locator.Sequence:
		return true
	default:
		return current.ID == locator.ID && current.AWGKeyEpoch == locator.AWGKeyEpoch &&
			current.AWGPublicKey == locator.AWGPublicKey
	}
}
