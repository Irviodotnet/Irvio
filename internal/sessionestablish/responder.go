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
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/protocol"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

type DescriptorBinding struct {
	SignedEnvelope []byte
	ID             [HashSize]byte
	Incarnation    uint64
	Sequence       uint64
	AWGKeyEpoch    uint64
	AWGPublicKey   [32]byte
}

type EndPeerContext struct {
	RemotePeer              peer.ID
	AttemptID               AttemptID
	OwnerToken              [OwnerTokenSize]byte
	RuntimeGeneration       uint64
	ClientNetworkGeneration uint64
	ExitNetworkGeneration   uint64
	Descriptor              DescriptorBinding
	ClientDescriptor        DescriptorBinding
}

// BindingFromSignedAnnouncement verifies original bytes and returns their rollback-resistant AWG binding.
func BindingFromSignedAnnouncement(signed []byte, expected peer.ID, now time.Time) (DescriptorBinding, error) {
	record, err := protocol.VerifySignedAnnouncementRecord(signed, expected, now)
	if err != nil || len(record.Announcement.AwgPublicKey) != 32 || record.Announcement.AwgKeyEpoch == 0 {
		return DescriptorBinding{}, ErrInvalid
	}
	var key [32]byte
	copy(key[:], record.Announcement.AwgPublicKey)
	return DescriptorBinding{
		SignedEnvelope: append([]byte(nil), signed...), ID: record.DescriptorID,
		Incarnation: record.Announcement.Incarnation, Sequence: record.Announcement.Sequence,
		AWGKeyEpoch: record.Announcement.AwgKeyEpoch, AWGPublicKey: key,
	}, nil
}

type AnswerResult struct {
	ICEAnswer            *irviov1.IceAnswer
	ProvisionalGrant     *irviov1.TunnelLease
	ExitControlAddresses [][]byte
	ExitControlPort      uint32
	AWGProfileHash       [HashSize]byte
	ActivationDeadline   time.Time
}

type PrepareAuthorizer interface {
	AuthorizePrepare(context.Context, EndPeerContext, *irviov1.SessionPrepare) error
}

type OfferProcessor interface {
	ProcessOffer(context.Context, EndPeerContext, *irviov1.SessionOffer) (AnswerResult, error)
}

type AllowCompatiblePeer struct{}

func (AllowCompatiblePeer) AuthorizePrepare(context.Context, EndPeerContext, *irviov1.SessionPrepare) error {
	return nil
}

type ResponderOptions struct {
	Local             peer.ID
	PrivateKey        crypto.PrivKey
	Descriptor        func() DescriptorBinding
	NetworkGeneration func() uint64
	Authorizer        PrepareAuthorizer
	Processor         OfferProcessor
	Policy            Policy
	MaxPending        int
	PendingLifetime   time.Duration
	Now               func() time.Time
	Random            io.Reader
}

type Responder struct {
	options ResponderOptions
	guard   *ReplayGuard
	context context.Context
	cancel  context.CancelFunc
	once    sync.Once
	wg      sync.WaitGroup

	mu      sync.Mutex
	pending map[AttemptID]*pendingAttempt
	closed  bool
}

type pendingAttempt struct {
	context   EndPeerContext
	prepare   Verified
	ready     Verified
	challenge [ChallengeSize]byte
	expires   time.Time
}

func NewResponder(options ResponderOptions) (*Responder, error) {
	if options.Local == "" || options.PrivateKey == nil || options.Descriptor == nil ||
		options.NetworkGeneration == nil || options.Authorizer == nil || options.Processor == nil {
		return nil, ErrInvalid
	}
	signer, err := peer.IDFromPrivateKey(options.PrivateKey)
	if err != nil || signer != options.Local {
		return nil, ErrWrongPeer
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.MaxPending == 0 {
		options.MaxPending = 256
	}
	if options.PendingLifetime == 0 {
		options.PendingLifetime = 20 * time.Second
	}
	if options.MaxPending < 1 || options.MaxPending > 65_536 ||
		options.PendingLifetime <= 0 || options.PendingLifetime > time.Minute {
		return nil, ErrInvalid
	}
	if options.Policy.MaxLifetime == 0 {
		options.Policy = DefaultPolicy()
	}
	guard, err := NewReplayGuard(2*options.MaxPending, options.PendingLifetime, options.Now)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	responder := &Responder{
		options: options, guard: guard, context: ctx, cancel: cancel,
		pending: make(map[AttemptID]*pendingAttempt),
	}
	responder.wg.Add(1)
	go responder.expiryLoop()
	return responder, nil
}

func (r *Responder) HandleSignal(ctx context.Context, _ peer.ID, wire []byte) ([]byte, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	message, err := VerifyForRecipient(wire, r.options.Local, r.options.Now(), r.options.Policy)
	if err != nil {
		return nil, err
	}
	switch message.Payload.Kind {
	case irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_PREPARE:
		return r.handlePrepare(ctx, message)
	case irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_OFFER:
		return r.handleOffer(ctx, message)
	case irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_CANCEL:
		return r.handleCancel(ctx, message)
	default:
		return nil, ErrInvalid
	}
}

func (r *Responder) handlePrepare(ctx context.Context, message Verified) ([]byte, error) {
	body := message.Payload.GetPrepare()
	descriptor := r.options.Descriptor()
	if descriptor.ID == ([HashSize]byte{}) || descriptor.Incarnation == 0 ||
		descriptor.Sequence == 0 || descriptor.AWGKeyEpoch == 0 || descriptor.AWGPublicKey == ([32]byte{}) ||
		!descriptorLocatorAccepted(body, descriptor) {
		return r.decline(message, 1)
	}
	endContext := endPeerContext(message, descriptor, r.options.NetworkGeneration())
	if err := r.options.Authorizer.AuthorizePrepare(ctx, endContext, body); err != nil {
		return r.decline(message, 2)
	}
	return r.guard.Execute(ctx, message, func(context.Context) ([]byte, error) {
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			return nil, ErrClosed
		}
		if len(r.pending) >= r.options.MaxPending {
			r.mu.Unlock()
			return nil, ErrCapacity
		}
		if _, exists := r.pending[endContext.AttemptID]; exists {
			r.mu.Unlock()
			return nil, ErrConflict
		}
		var challenge [ChallengeSize]byte
		if _, err := io.ReadFull(r.options.Random, challenge[:]); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		if challenge == ([ChallengeSize]byte{}) {
			challenge[len(challenge)-1] = 1
		}
		now := r.options.Now()
		expires := now.Add(r.options.Policy.ReadyLifetime)
		if requestExpiry := time.UnixMilli(message.Payload.ExpiresAtUnixMilli); requestExpiry.Before(expires) {
			expires = requestExpiry
		}
		readyPayload := &irviov1.SessionEstablishPayload{
			ProtocolVersion: ProtocolVersion,
			Kind:            irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_READY,
			SenderPeerId:    []byte(r.options.Local), RecipientPeerId: []byte(message.Signer),
			AttemptId:          append([]byte(nil), message.Payload.AttemptId...),
			OwnerToken:         append([]byte(nil), message.Payload.OwnerToken...),
			PredecessorHash:    append([]byte(nil), message.Hash[:]...),
			CreatedAtUnixMilli: now.UnixMilli(), ExpiresAtUnixMilli: expires.UnixMilli(),
			Body: &irviov1.SessionEstablishPayload_Ready{Ready: &irviov1.SessionReady{
				PrepareHash:        append([]byte(nil), message.Hash[:]...),
				SignedAnnouncement: append([]byte(nil), descriptor.SignedEnvelope...),
				ExitChallenge:      append([]byte(nil), challenge[:]...),
				NetworkGeneration:  endContext.ExitNetworkGeneration,
				AwgKeyEpoch:        descriptor.AWGKeyEpoch,
			}},
		}
		readyWire, err := Seal(r.options.PrivateKey, readyPayload, now, r.options.Policy)
		if err != nil {
			r.mu.Unlock()
			return nil, err
		}
		ready, err := Verify(readyWire, r.options.Local, message.Signer, now, r.options.Policy)
		if err != nil {
			r.mu.Unlock()
			return nil, err
		}
		r.pending[endContext.AttemptID] = &pendingAttempt{
			context: endContext, prepare: message, ready: ready, challenge: challenge,
			expires: minTime(expires, now.Add(r.options.PendingLifetime)),
		}
		r.mu.Unlock()
		return readyWire, nil
	})
}

func descriptorLocatorAccepted(body *irviov1.SessionPrepare, current DescriptorBinding) bool {
	if body == nil || len(body.DescriptorId) != HashSize || body.DescriptorIncarnation == 0 || body.DescriptorSequence == 0 {
		return false
	}
	switch {
	case body.DescriptorIncarnation > current.Incarnation:
		return false
	case body.DescriptorIncarnation < current.Incarnation:
		return true
	case body.DescriptorSequence > current.Sequence:
		return false
	case body.DescriptorSequence < current.Sequence:
		return true
	default:
		return bytes.Equal(body.DescriptorId, current.ID[:])
	}
}

func (r *Responder) handleOffer(ctx context.Context, message Verified) ([]byte, error) {
	var attempt AttemptID
	copy(attempt[:], message.Payload.AttemptId)
	r.mu.Lock()
	pending := r.pending[attempt]
	if pending == nil || !pending.expires.After(r.options.Now()) {
		r.mu.Unlock()
		return nil, ErrExpired
	}
	snapshot := *pending
	r.mu.Unlock()
	if message.Signer != snapshot.context.RemotePeer ||
		RequirePredecessor(message, snapshot.ready) != nil ||
		!bytes.Equal(message.Payload.GetOffer().ExitChallenge, snapshot.challenge[:]) ||
		message.Payload.GetOffer().ExitAwgKeyEpoch != snapshot.context.Descriptor.AWGKeyEpoch {
		return nil, ErrChain
	}
	clientDescriptor, err := BindingFromSignedAnnouncement(
		message.Payload.GetOffer().ClientSignedAnnouncement, message.Signer, r.options.Now(),
	)
	if err != nil || clientDescriptor.AWGKeyEpoch != message.Payload.GetOffer().ClientAwgKeyEpoch {
		return nil, ErrConflict
	}
	snapshot.context.ClientDescriptor = clientDescriptor
	return r.guard.Execute(ctx, message, func(ctx context.Context) ([]byte, error) {
		result, err := r.options.Processor.ProcessOffer(ctx, snapshot.context, message.Payload.GetOffer())
		if err != nil {
			return r.decline(message, 3)
		}
		if err := validateAnswerResult(result, r.options.Now()); err != nil {
			return nil, err
		}
		now := r.options.Now()
		answerPayload := &irviov1.SessionEstablishPayload{
			ProtocolVersion: ProtocolVersion,
			Kind:            irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_ANSWER,
			SenderPeerId:    []byte(r.options.Local), RecipientPeerId: []byte(message.Signer),
			AttemptId:          append([]byte(nil), message.Payload.AttemptId...),
			OwnerToken:         append([]byte(nil), message.Payload.OwnerToken...),
			PredecessorHash:    append([]byte(nil), message.Hash[:]...),
			CreatedAtUnixMilli: now.UnixMilli(),
			ExpiresAtUnixMilli: minTime(now.Add(r.options.Policy.MaxLifetime), result.ActivationDeadline).UnixMilli(),
			Body: &irviov1.SessionEstablishPayload_Answer{Answer: &irviov1.SessionAnswer{
				OfferHash: append([]byte(nil), message.Hash[:]...), IceAnswer: result.ICEAnswer,
				ProvisionalGrant:       result.ProvisionalGrant,
				ExitControlAddresses:   cloneBytes(result.ExitControlAddresses),
				ExitControlPort:        result.ExitControlPort,
				AwgProfileHash:         append([]byte(nil), result.AWGProfileHash[:]...),
				ActivationDeadlineUnix: result.ActivationDeadline.Unix(),
			}},
		}
		answer, err := Seal(r.options.PrivateKey, answerPayload, now, r.options.Policy)
		if err == nil {
			r.mu.Lock()
			delete(r.pending, attempt)
			r.mu.Unlock()
		}
		return answer, err
	})
}

func (r *Responder) handleCancel(ctx context.Context, message Verified) ([]byte, error) {
	var attempt AttemptID
	copy(attempt[:], message.Payload.AttemptId)
	r.mu.Lock()
	pending := r.pending[attempt]
	if pending != nil && pending.context.RemotePeer == message.Signer &&
		RequirePredecessor(message, pending.ready) == nil {
		delete(r.pending, attempt)
	}
	r.mu.Unlock()
	return r.guard.Execute(ctx, message, func(context.Context) ([]byte, error) {
		return r.cancelResponse(message, 1)
	})
}

func (r *Responder) decline(predecessor Verified, reason uint32) ([]byte, error) {
	now := r.options.Now()
	return Seal(r.options.PrivateKey, &irviov1.SessionEstablishPayload{
		ProtocolVersion: ProtocolVersion,
		Kind:            irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_DECLINE,
		SenderPeerId:    []byte(r.options.Local), RecipientPeerId: []byte(predecessor.Signer),
		AttemptId:          append([]byte(nil), predecessor.Payload.AttemptId...),
		OwnerToken:         append([]byte(nil), predecessor.Payload.OwnerToken...),
		PredecessorHash:    append([]byte(nil), predecessor.Hash[:]...),
		CreatedAtUnixMilli: now.UnixMilli(),
		ExpiresAtUnixMilli: now.Add(r.options.Policy.ReadyLifetime).UnixMilli(),
		Body:               &irviov1.SessionEstablishPayload_Decline{Decline: &irviov1.SessionDecline{Reason: reason}},
	}, now, r.options.Policy)
}

func (r *Responder) cancelResponse(predecessor Verified, reason uint32) ([]byte, error) {
	now := r.options.Now()
	return Seal(r.options.PrivateKey, &irviov1.SessionEstablishPayload{
		ProtocolVersion: ProtocolVersion,
		Kind:            irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_CANCEL,
		SenderPeerId:    []byte(r.options.Local), RecipientPeerId: []byte(predecessor.Signer),
		AttemptId:          append([]byte(nil), predecessor.Payload.AttemptId...),
		OwnerToken:         append([]byte(nil), predecessor.Payload.OwnerToken...),
		PredecessorHash:    append([]byte(nil), predecessor.Hash[:]...),
		CreatedAtUnixMilli: now.UnixMilli(),
		ExpiresAtUnixMilli: now.Add(r.options.Policy.ReadyLifetime).UnixMilli(),
		Body:               &irviov1.SessionEstablishPayload_Cancel{Cancel: &irviov1.SessionCancel{Reason: reason}},
	}, now, r.options.Policy)
}

func endPeerContext(message Verified, descriptor DescriptorBinding, exitGeneration uint64) EndPeerContext {
	var attempt AttemptID
	var owner [OwnerTokenSize]byte
	copy(attempt[:], message.Payload.AttemptId)
	copy(owner[:], message.Payload.OwnerToken)
	prepare := message.Payload.GetPrepare()
	return EndPeerContext{
		RemotePeer: message.Signer, AttemptID: attempt, OwnerToken: owner,
		RuntimeGeneration:       prepare.RuntimeGeneration,
		ClientNetworkGeneration: prepare.NetworkGeneration,
		ExitNetworkGeneration:   exitGeneration, Descriptor: descriptor,
	}
}

func validateAnswerResult(result AnswerResult, now time.Time) error {
	if result.ICEAnswer == nil || result.ProvisionalGrant == nil ||
		len(result.ExitControlAddresses) == 0 || len(result.ExitControlAddresses) > 4 ||
		result.ExitControlPort == 0 || result.ExitControlPort > 65535 ||
		result.AWGProfileHash == ([HashSize]byte{}) ||
		!result.ActivationDeadline.After(now) || result.ActivationDeadline.After(now.Add(30*time.Second)) {
		return ErrInvalid
	}
	for _, address := range result.ExitControlAddresses {
		if len(address) != 4 && len(address) != 16 {
			return ErrInvalid
		}
	}
	return nil
}

func cloneBytes(values [][]byte) [][]byte {
	result := make([][]byte, len(values))
	for index, value := range values {
		result[index] = append([]byte(nil), value...)
	}
	return result
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (r *Responder) expiryLoop() {
	defer r.wg.Done()
	interval := r.options.PendingLifetime / 2
	if interval > time.Second {
		interval = time.Second
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			now := r.options.Now()
			r.mu.Lock()
			for attempt, pending := range r.pending {
				if !pending.expires.After(now) {
					delete(r.pending, attempt)
				}
			}
			r.mu.Unlock()
		case <-r.context.Done():
			return
		}
	}
}

func (r *Responder) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

func (r *Responder) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.mu.Lock()
		r.closed = true
		clear(r.pending)
		r.mu.Unlock()
		r.guard.Close()
		r.cancel()
		r.wg.Wait()
	})
}

var _ interface {
	HandleSignal(context.Context, peer.ID, []byte) ([]byte, error)
} = (*Responder)(nil)
