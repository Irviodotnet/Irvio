// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package sessionestablish authenticates direct and brokered end-peer transcripts.
package sessionestablish

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

const (
	ProtocolVersion  uint32 = 1
	DirectProtocolID        = "/hydra/session-establish/1.0.0"
	AttemptIDSize           = 16
	OwnerTokenSize          = 16
	HashSize                = sha256.Size
	ChallengeSize           = 32
	MaxWireSize             = 48 * 1024
	MaxPayloadSize          = 40 * 1024

	signatureDomain = "hydra/session-establish/signature/1.0.0\x00"
	hashDomain      = "hydra/session-establish/transcript-hash/1.0.0\x00"
)

var (
	ErrInvalid   = errors.New("invalid session-establishment message")
	ErrWrongPeer = errors.New("session-establishment peer mismatch")
	ErrSignature = errors.New("invalid session-establishment signature")
	ErrExpired   = errors.New("session-establishment message expired")
	ErrChain     = errors.New("invalid session-establishment predecessor")
	ErrReplay    = errors.New("session-establishment replay")
	ErrConflict  = errors.New("conflicting session-establishment message")
	ErrCapacity  = errors.New("session-establishment capacity busy")
	ErrClosed    = errors.New("session-establishment closed")
)

type AttemptID [AttemptIDSize]byte

func NewAttemptID(reader io.Reader) (AttemptID, error) {
	var id AttemptID
	if reader == nil {
		reader = rand.Reader
	}
	if _, err := io.ReadFull(reader, id[:]); err != nil {
		return id, err
	}
	if id == (AttemptID{}) {
		id[len(id)-1] = 1
	}
	return id, nil
}

type Policy struct {
	ClockSkew       time.Duration
	MaxLifetime     time.Duration
	ReadyLifetime   time.Duration
	PrepareLifetime time.Duration
}

func DefaultPolicy() Policy {
	return Policy{
		ClockSkew: time.Minute, MaxLifetime: 20 * time.Second,
		ReadyLifetime: 1500 * time.Millisecond, PrepareLifetime: 1500 * time.Millisecond,
	}
}

type Verified struct {
	Payload    *irviov1.SessionEstablishPayload
	Signer     peer.ID
	RawPayload []byte
	Wire       []byte
	Hash       [HashSize]byte
}

func Seal(privateKey crypto.PrivKey, payload *irviov1.SessionEstablishPayload, now time.Time, policy Policy) ([]byte, error) {
	if privateKey == nil {
		return nil, ErrInvalid
	}
	signer, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	if err := validatePayload(payload, signer, "", now, policy); err != nil {
		return nil, err
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil || len(raw) == 0 || len(raw) > MaxPayloadSize {
		return nil, ErrInvalid
	}
	signatureInput := make([]byte, 0, len(signatureDomain)+len(raw))
	signatureInput = append(signatureInput, signatureDomain...)
	signatureInput = append(signatureInput, raw...)
	signature, err := privateKey.Sign(signatureInput)
	if err != nil {
		return nil, fmt.Errorf("sign session transcript: %w", err)
	}
	publicKey, err := crypto.MarshalPublicKey(privateKey.GetPublic())
	if err != nil {
		return nil, fmt.Errorf("marshal transcript public key: %w", err)
	}
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(&irviov1.SignedSessionEstablish{
		Payload: raw, SignerPublicKey: publicKey, Signature: signature,
	})
	if err != nil || len(wire) == 0 || len(wire) > MaxWireSize {
		return nil, ErrInvalid
	}
	return wire, nil
}

func Verify(wire []byte, expectedSender, expectedRecipient peer.ID, now time.Time, policy Policy) (Verified, error) {
	if len(wire) == 0 || len(wire) > MaxWireSize || expectedSender == "" || expectedRecipient == "" {
		return Verified{}, ErrInvalid
	}
	signed := new(irviov1.SignedSessionEstablish)
	if err := (proto.UnmarshalOptions{RecursionLimit: 32, DiscardUnknown: false}).Unmarshal(wire, signed); err != nil {
		return Verified{}, ErrInvalid
	}
	if len(signed.Payload) == 0 || len(signed.Payload) > MaxPayloadSize ||
		len(signed.SignerPublicKey) == 0 || len(signed.Signature) == 0 {
		return Verified{}, ErrInvalid
	}
	publicKey, err := crypto.UnmarshalPublicKey(signed.SignerPublicKey)
	if err != nil {
		return Verified{}, ErrSignature
	}
	signer, err := peer.IDFromPublicKey(publicKey)
	if err != nil || signer != expectedSender {
		return Verified{}, ErrWrongPeer
	}
	signatureInput := make([]byte, 0, len(signatureDomain)+len(signed.Payload))
	signatureInput = append(signatureInput, signatureDomain...)
	signatureInput = append(signatureInput, signed.Payload...)
	ok, err := publicKey.Verify(signatureInput, signed.Signature)
	if err != nil || !ok {
		return Verified{}, ErrSignature
	}
	payload := new(irviov1.SessionEstablishPayload)
	if err := (proto.UnmarshalOptions{RecursionLimit: 24, DiscardUnknown: false}).Unmarshal(signed.Payload, payload); err != nil {
		return Verified{}, ErrInvalid
	}
	if err := validatePayload(payload, signer, expectedRecipient, now, policy); err != nil {
		return Verified{}, err
	}
	return Verified{
		Payload: payload, Signer: signer, RawPayload: append([]byte(nil), signed.Payload...),
		Wire: append([]byte(nil), wire...), Hash: TranscriptHash(wire),
	}, nil
}

// VerifyForRecipient authenticates the sender and binds the expected recipient.
func VerifyForRecipient(wire []byte, expectedRecipient peer.ID, now time.Time, policy Policy) (Verified, error) {
	if len(wire) == 0 || len(wire) > MaxWireSize || expectedRecipient == "" {
		return Verified{}, ErrInvalid
	}
	signed := new(irviov1.SignedSessionEstablish)
	if err := (proto.UnmarshalOptions{RecursionLimit: 32, DiscardUnknown: false}).Unmarshal(wire, signed); err != nil {
		return Verified{}, ErrInvalid
	}
	publicKey, err := crypto.UnmarshalPublicKey(signed.SignerPublicKey)
	if err != nil {
		return Verified{}, ErrSignature
	}
	sender, err := peer.IDFromPublicKey(publicKey)
	if err != nil {
		return Verified{}, ErrSignature
	}
	return Verify(wire, sender, expectedRecipient, now, policy)
}

func TranscriptHash(wire []byte) [HashSize]byte {
	digest := sha256.New()
	_, _ = digest.Write([]byte(hashDomain))
	_, _ = digest.Write(wire)
	var result [HashSize]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func RequirePredecessor(message Verified, predecessor Verified) error {
	if message.Payload == nil || predecessor.Payload == nil ||
		!bytes.Equal(message.Payload.PredecessorHash, predecessor.Hash[:]) ||
		!bytes.Equal(message.Payload.AttemptId, predecessor.Payload.AttemptId) ||
		!bytes.Equal(message.Payload.OwnerToken, predecessor.Payload.OwnerToken) ||
		message.Payload.SenderPeerId == nil || message.Payload.RecipientPeerId == nil ||
		!bytes.Equal(message.Payload.SenderPeerId, predecessor.Payload.RecipientPeerId) ||
		!bytes.Equal(message.Payload.RecipientPeerId, predecessor.Payload.SenderPeerId) {
		return ErrChain
	}
	return nil
}

func validatePayload(payload *irviov1.SessionEstablishPayload, signer, expectedRecipient peer.ID, now time.Time, policy Policy) error {
	if policy.MaxLifetime <= 0 || policy.MaxLifetime > time.Minute ||
		policy.ReadyLifetime <= 0 || policy.PrepareLifetime <= 0 || policy.ClockSkew < 0 {
		return ErrInvalid
	}
	if payload == nil || payload.ProtocolVersion != ProtocolVersion ||
		len(payload.AttemptId) != AttemptIDSize || len(payload.OwnerToken) != OwnerTokenSize ||
		payload.CreatedAtUnixMilli <= 0 || payload.ExpiresAtUnixMilli <= 0 {
		return ErrInvalid
	}
	var attempt AttemptID
	copy(attempt[:], payload.AttemptId)
	if attempt == (AttemptID{}) || bytes.Equal(payload.OwnerToken, make([]byte, OwnerTokenSize)) {
		return ErrInvalid
	}
	sender, err := peer.IDFromBytes(payload.SenderPeerId)
	if err != nil || sender != signer {
		return ErrWrongPeer
	}
	recipient, err := peer.IDFromBytes(payload.RecipientPeerId)
	if err != nil || recipient == sender {
		return ErrWrongPeer
	}
	if expectedRecipient != "" && recipient != expectedRecipient {
		return ErrWrongPeer
	}
	created := time.UnixMilli(payload.CreatedAtUnixMilli)
	expires := time.UnixMilli(payload.ExpiresAtUnixMilli)
	if !expires.After(created) || expires.Sub(created) > policy.MaxLifetime ||
		created.After(now.Add(policy.ClockSkew)) {
		return ErrInvalid
	}
	if !expires.After(now) {
		return ErrExpired
	}
	predecessorLength := len(payload.PredecessorHash)
	if payload.Kind == irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_PREPARE {
		if predecessorLength != 0 {
			return ErrChain
		}
	} else if predecessorLength != HashSize {
		return ErrChain
	}
	switch body := payload.Body.(type) {
	case *irviov1.SessionEstablishPayload_Prepare:
		if payload.Kind != irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_PREPARE ||
			body.Prepare == nil || !validCountry(body.Prepare.CountryIntent) ||
			len(body.Prepare.DescriptorId) != HashSize || body.Prepare.DescriptorIncarnation == 0 ||
			body.Prepare.DescriptorSequence == 0 || body.Prepare.NetworkGeneration == 0 ||
			body.Prepare.RuntimeGeneration == 0 ||
			body.Prepare.Purpose != irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT ||
			expires.Sub(created) > policy.PrepareLifetime {
			return ErrInvalid
		}
	case *irviov1.SessionEstablishPayload_Ready:
		if payload.Kind != irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_READY ||
			body.Ready == nil || len(body.Ready.PrepareHash) != HashSize ||
			!bytes.Equal(body.Ready.PrepareHash, payload.PredecessorHash) ||
			len(body.Ready.SignedAnnouncement) == 0 || len(body.Ready.SignedAnnouncement) > 8*1024 ||
			len(body.Ready.ExitChallenge) != ChallengeSize || body.Ready.NetworkGeneration == 0 ||
			body.Ready.AwgKeyEpoch == 0 || expires.Sub(created) > policy.ReadyLifetime {
			return ErrInvalid
		}
	case *irviov1.SessionEstablishPayload_Offer:
		if payload.Kind != irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_OFFER ||
			body.Offer == nil || len(body.Offer.ReadyHash) != HashSize ||
			!bytes.Equal(body.Offer.ReadyHash, payload.PredecessorHash) ||
			len(body.Offer.ExitChallenge) != ChallengeSize || body.Offer.IceOffer == nil ||
			body.Offer.ClientAwgKeyEpoch == 0 || body.Offer.ExitAwgKeyEpoch == 0 ||
			len(body.Offer.AwgProfileHash) != HashSize ||
			len(body.Offer.ClientSignedAnnouncement) == 0 || len(body.Offer.ClientSignedAnnouncement) > 8*1024 ||
			(!body.Offer.RequestIpv4 && !body.Offer.RequestIpv6) ||
			body.Offer.IceOffer.GetDescription() == nil ||
			len(body.Offer.IceOffer.GetDescription().GetCandidates()) == 0 ||
			len(body.Offer.IceOffer.GetDescription().GetCandidates()) > 16 {
			return ErrInvalid
		}
	case *irviov1.SessionEstablishPayload_Answer:
		if payload.Kind != irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_ANSWER ||
			body.Answer == nil || len(body.Answer.OfferHash) != HashSize ||
			!bytes.Equal(body.Answer.OfferHash, payload.PredecessorHash) ||
			body.Answer.IceAnswer == nil || body.Answer.ProvisionalGrant == nil ||
			len(body.Answer.ExitControlAddresses) == 0 || len(body.Answer.ExitControlAddresses) > 4 ||
			body.Answer.ExitControlPort == 0 || body.Answer.ExitControlPort > 65535 ||
			len(body.Answer.AwgProfileHash) != HashSize ||
			body.Answer.ActivationDeadlineUnix <= now.Unix() {
			return ErrInvalid
		}
	case *irviov1.SessionEstablishPayload_Decline:
		if payload.Kind != irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_DECLINE || body.Decline == nil || body.Decline.Reason == 0 {
			return ErrInvalid
		}
	case *irviov1.SessionEstablishPayload_Cancel:
		if payload.Kind != irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_CANCEL || body.Cancel == nil || body.Cancel.Reason == 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validCountry(country string) bool {
	return len(country) == 2 && country[0] >= 'A' && country[0] <= 'Z' &&
		country[1] >= 'A' && country[1] <= 'Z'
}
