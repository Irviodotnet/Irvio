// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"fmt"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"
	"google.golang.org/protobuf/proto"

	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

const (
	ReceiptDomain = "hydra/rendezvous-registration-receipt"
	receiptCodec  = "\x82\x80\xc0\x01"
)

type Receipt struct {
	ServerPeer        peer.ID
	ServerIncarnation uint64
	SubjectPeer       peer.ID
	RegistrationID    RegistrationID
	Sequence          uint64
	DescriptorID      [32]byte
	DescriptorVersion DescriptorVersion
	ObservedIP        netip.Addr
	ObservedFamily    irviov1.ReachabilityFamily
	Country           CountryObservation
	IssuedAt          time.Time
	ExpiresAt         time.Time
	ServerPublicProof []byte
}

type SignedReceipt struct {
	Receipt        Receipt
	SignedEnvelope []byte
	RawPayload     []byte
}

type receiptRecord struct {
	message *irviov1.RegistrationReceiptPayload
}

func (*receiptRecord) Domain() string { return ReceiptDomain }
func (*receiptRecord) Codec() []byte  { return []byte(receiptCodec) }

func (r *receiptRecord) MarshalRecord() ([]byte, error) {
	if r == nil || r.message == nil {
		return nil, ErrInvalid
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(r.message)
}

func (r *receiptRecord) UnmarshalRecord(payload []byte) error {
	if len(payload) == 0 || len(payload) > MaxReceiptSize {
		return fmt.Errorf("%w: receipt payload size", ErrInvalid)
	}
	message := new(irviov1.RegistrationReceiptPayload)
	if err := (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(payload, message); err != nil {
		return fmt.Errorf("%w: receipt protobuf", ErrInvalid)
	}
	r.message = message
	return nil
}

func SealReceipt(privateKey crypto.PrivKey, receipt Receipt, now time.Time) (SignedReceipt, error) {
	message, err := receiptToProto(receipt)
	if err != nil {
		return SignedReceipt{}, err
	}
	if err := validateReceipt(receipt, now); err != nil {
		return SignedReceipt{}, err
	}
	envelope, err := record.Seal(&receiptRecord{message: message}, privateKey)
	if err != nil {
		return SignedReceipt{}, fmt.Errorf("seal registration receipt: %w", err)
	}
	signed, err := envelope.Marshal()
	if err != nil {
		return SignedReceipt{}, fmt.Errorf("marshal registration receipt: %w", err)
	}
	if len(signed) > MaxReceiptSize {
		return SignedReceipt{}, fmt.Errorf("%w: signed receipt size %d", ErrInvalid, len(signed))
	}
	return SignedReceipt{
		Receipt: receipt, SignedEnvelope: signed,
		RawPayload: append([]byte(nil), envelope.RawPayload...),
	}, nil
}

func VerifyReceipt(signed []byte, expectedServer peer.ID, now time.Time) (SignedReceipt, error) {
	if len(signed) == 0 || len(signed) > MaxReceiptSize || expectedServer == "" {
		return SignedReceipt{}, fmt.Errorf("%w: receipt envelope", ErrInvalid)
	}
	destination := new(receiptRecord)
	envelope, err := record.ConsumeTypedEnvelope(signed, destination)
	if err != nil {
		return SignedReceipt{}, fmt.Errorf("verify registration receipt: %w", err)
	}
	if !bytes.Equal(envelope.PayloadType, []byte(receiptCodec)) {
		return SignedReceipt{}, fmt.Errorf("%w: receipt codec", ErrInvalid)
	}
	signer, err := peer.IDFromPublicKey(envelope.PublicKey)
	if err != nil || signer != expectedServer {
		return SignedReceipt{}, fmt.Errorf("%w: receipt signer", ErrWrongPeer)
	}
	receipt, err := receiptFromProto(destination.message)
	if err != nil {
		return SignedReceipt{}, err
	}
	if receipt.ServerPeer != signer {
		return SignedReceipt{}, fmt.Errorf("%w: receipt server", ErrWrongPeer)
	}
	if err := validateReceipt(receipt, now); err != nil {
		return SignedReceipt{}, err
	}
	return SignedReceipt{
		Receipt: receipt, SignedEnvelope: append([]byte(nil), signed...),
		RawPayload: append([]byte(nil), envelope.RawPayload...),
	}, nil
}

// VerifyReceiptAny authenticates the signer; callers must still verify allowed owner routes.
func VerifyReceiptAny(signed []byte, now time.Time) (SignedReceipt, error) {
	if len(signed) == 0 || len(signed) > MaxReceiptSize {
		return SignedReceipt{}, fmt.Errorf("%w: receipt envelope", ErrInvalid)
	}
	envelope, err := record.UnmarshalEnvelope(signed)
	if err != nil {
		return SignedReceipt{}, fmt.Errorf("decode registration receipt signer: %w", err)
	}
	server, err := peer.IDFromPublicKey(envelope.PublicKey)
	if err != nil {
		return SignedReceipt{}, fmt.Errorf("%w: receipt signer", ErrWrongPeer)
	}
	return VerifyReceipt(signed, server, now)
}

func validateReceipt(value Receipt, now time.Time) error {
	if value.ServerPeer == "" || value.SubjectPeer == "" || value.ServerPeer == value.SubjectPeer ||
		value.ServerIncarnation == 0 || value.RegistrationID == (RegistrationID{}) ||
		value.Sequence == 0 || value.DescriptorID == ([32]byte{}) ||
		!value.DescriptorVersion.Valid() || !value.ObservedIP.IsValid() ||
		value.ObservedFamily != addressFamily(value.ObservedIP) ||
		value.IssuedAt.Unix() <= 0 || !value.ExpiresAt.After(value.IssuedAt) ||
		value.ExpiresAt.Sub(value.IssuedAt) > 10*time.Minute ||
		value.IssuedAt.After(now.Add(5*time.Minute)) || !value.ExpiresAt.After(now) ||
		len(value.ServerPublicProof) > 1024 {
		return fmt.Errorf("%w: registration receipt", ErrInvalid)
	}
	if value.Country.ObservedIP != value.ObservedIP || value.Country.Family != value.ObservedFamily {
		return fmt.Errorf("%w: country observation binding", ErrInvalid)
	}
	if value.Country.State == CountryObserved && len(value.Country.Country) != 2 {
		return fmt.Errorf("%w: observed country", ErrInvalid)
	}
	return nil
}

func receiptToProto(value Receipt) (*irviov1.RegistrationReceiptPayload, error) {
	if err := validateReceipt(value, value.IssuedAt); err != nil {
		return nil, err
	}
	return &irviov1.RegistrationReceiptPayload{
		ProtocolVersion: ProtocolVersion,
		ServerPeerId:    []byte(value.ServerPeer), ServerIncarnation: value.ServerIncarnation,
		SubjectPeerId: []byte(value.SubjectPeer), RegistrationId: append([]byte(nil), value.RegistrationID[:]...),
		ReceiptSequence: value.Sequence, DescriptorId: append([]byte(nil), value.DescriptorID[:]...),
		DescriptorIncarnation: value.DescriptorVersion.Incarnation, DescriptorSequence: value.DescriptorVersion.Sequence,
		ObservedIp: append([]byte(nil), value.ObservedIP.AsSlice()...), ObservedFamily: value.ObservedFamily,
		Country:      observationToProto(value.Country),
		IssuedAtUnix: value.IssuedAt.Unix(), ExpiresAtUnix: value.ExpiresAt.Unix(),
		ServerPublicProof: append([]byte(nil), value.ServerPublicProof...),
	}, nil
}

func receiptFromProto(message *irviov1.RegistrationReceiptPayload) (Receipt, error) {
	if message == nil || message.ProtocolVersion != ProtocolVersion ||
		len(message.RegistrationId) != MaxRegistrationIDSize || len(message.DescriptorId) != 32 {
		return Receipt{}, fmt.Errorf("%w: receipt fields", ErrInvalid)
	}
	server, err := peer.IDFromBytes(message.ServerPeerId)
	if err != nil {
		return Receipt{}, fmt.Errorf("%w: receipt server", ErrInvalid)
	}
	subject, err := peer.IDFromBytes(message.SubjectPeerId)
	if err != nil {
		return Receipt{}, fmt.Errorf("%w: receipt subject", ErrInvalid)
	}
	observed, ok := netip.AddrFromSlice(message.ObservedIp)
	if !ok || observed.Is4In6() {
		return Receipt{}, fmt.Errorf("%w: receipt observed IP", ErrInvalid)
	}
	var registrationID RegistrationID
	copy(registrationID[:], message.RegistrationId)
	var descriptorID [32]byte
	copy(descriptorID[:], message.DescriptorId)
	country, err := observationFromProto(message.Country)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{
		ServerPeer: server, ServerIncarnation: message.ServerIncarnation,
		SubjectPeer: subject, RegistrationID: registrationID, Sequence: message.ReceiptSequence,
		DescriptorID:      descriptorID,
		DescriptorVersion: DescriptorVersion{Incarnation: message.DescriptorIncarnation, Sequence: message.DescriptorSequence},
		ObservedIP:        observed, ObservedFamily: message.ObservedFamily, Country: country,
		IssuedAt: time.Unix(message.IssuedAtUnix, 0).UTC(), ExpiresAt: time.Unix(message.ExpiresAtUnix, 0).UTC(),
		ServerPublicProof: append([]byte(nil), message.ServerPublicProof...),
	}, nil
}

func observationToProto(value CountryObservation) *irviov1.CountryObservation {
	state := irviov1.CountryObservationState_COUNTRY_OBSERVATION_STATE_UNKNOWN
	if value.State == CountryObserved {
		state = irviov1.CountryObservationState_COUNTRY_OBSERVATION_STATE_OBSERVED
	} else if value.State == CountryConflicting {
		state = irviov1.CountryObservationState_COUNTRY_OBSERVATION_STATE_CONFLICTING
	}
	return &irviov1.CountryObservation{
		State: state, Country: value.Country, ObservedIp: append([]byte(nil), value.ObservedIP.AsSlice()...),
		Family: value.Family, DatabaseType: value.DatabaseType,
		DatabaseBuildUnix: value.DatabaseBuild.Unix(), ObservedAtUnix: value.ObservedAt.Unix(),
	}
}

func observationFromProto(message *irviov1.CountryObservation) (CountryObservation, error) {
	if message == nil {
		return CountryObservation{}, fmt.Errorf("%w: country observation", ErrInvalid)
	}
	address, ok := netip.AddrFromSlice(message.ObservedIp)
	if !ok || address.Is4In6() || addressFamily(address) != message.Family {
		return CountryObservation{}, fmt.Errorf("%w: country observation IP", ErrInvalid)
	}
	state := CountryUnknown
	if message.State == irviov1.CountryObservationState_COUNTRY_OBSERVATION_STATE_OBSERVED {
		state = CountryObserved
	} else if message.State == irviov1.CountryObservationState_COUNTRY_OBSERVATION_STATE_CONFLICTING {
		state = CountryConflicting
	} else if message.State != irviov1.CountryObservationState_COUNTRY_OBSERVATION_STATE_UNKNOWN {
		return CountryObservation{}, fmt.Errorf("%w: country observation state", ErrInvalid)
	}
	return CountryObservation{
		State: state, Country: message.Country, ObservedIP: address, Family: message.Family,
		DatabaseType: message.DatabaseType, DatabaseBuild: time.Unix(message.DatabaseBuildUnix, 0).UTC(),
		ObservedAt: time.Unix(message.ObservedAtUnix, 0).UTC(),
	}, nil
}
