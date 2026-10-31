// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"crypto/sha256"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"
	"google.golang.org/protobuf/proto"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

const (
	PrivateReceiptDomain = "irvio/private-registration-receipt/v1"
	privateReceiptCodec  = "\x83\x80\xc0\x01"
)

func LocatorCommitment(locator [32]byte) [32]byte {
	return sha256.Sum256(append([]byte("IRVIO-PRIVATE-LOCATOR-V1\x00"), locator[:]...))
}

func verifyRegistrationReceipt(signed []byte, server peer.ID, locator [32]byte, now time.Time) (SignedReceipt, error) {
	if locator != ([32]byte{}) {
		return VerifyPrivateReceipt(signed, server, locator, now)
	}
	return VerifyReceipt(signed, server, now)
}

type privateReceiptRecord struct {
	message *irviov1.PrivateRegistrationReceiptPayload
}

func (*privateReceiptRecord) Domain() string { return PrivateReceiptDomain }
func (*privateReceiptRecord) Codec() []byte  { return []byte(privateReceiptCodec) }
func (r *privateReceiptRecord) MarshalRecord() ([]byte, error) {
	if r == nil || r.message == nil {
		return nil, ErrInvalid
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(r.message)
}
func (r *privateReceiptRecord) UnmarshalRecord(data []byte) error {
	if len(data) == 0 || len(data) > MaxReceiptSize {
		return ErrInvalid
	}
	r.message = new(irviov1.PrivateRegistrationReceiptPayload)
	return (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(data, r.message)
}

func SealPrivateReceipt(key crypto.PrivKey, value Receipt, locator [32]byte, now time.Time) (SignedReceipt, error) {
	if key == nil || locator == ([32]byte{}) {
		return SignedReceipt{}, ErrInvalid
	}
	commitment := LocatorCommitment(locator)
	if value.PrivateLocatorCommitment != ([32]byte{}) && value.PrivateLocatorCommitment != commitment {
		return SignedReceipt{}, ErrInvalid
	}
	if err := validateReceipt(value, now); err != nil {
		return SignedReceipt{}, err
	}
	server, err := peer.IDFromPrivateKey(key)
	if err != nil || server != value.ServerPeer {
		return SignedReceipt{}, ErrWrongPeer
	}
	message, err := receiptToProto(value)
	if err != nil {
		return SignedReceipt{}, err
	}
	envelope, err := record.Seal(&privateReceiptRecord{message: &irviov1.PrivateRegistrationReceiptPayload{
		ProtocolVersion: ProtocolVersion, LocatorCommitment: commitment[:], Receipt: message,
	}}, key)
	if err != nil {
		return SignedReceipt{}, err
	}
	signed, err := envelope.Marshal()
	if err != nil || len(signed) > MaxReceiptSize {
		return SignedReceipt{}, ErrInvalid
	}
	value.PrivateLocatorCommitment = commitment
	return SignedReceipt{Receipt: value, SignedEnvelope: signed, RawPayload: append([]byte(nil), envelope.RawPayload...)}, nil
}

func VerifyPrivateReceipt(signed []byte, server peer.ID, locator [32]byte, now time.Time) (SignedReceipt, error) {
	if len(signed) == 0 || len(signed) > MaxReceiptSize || server == "" || locator == ([32]byte{}) {
		return SignedReceipt{}, ErrInvalid
	}
	destination := new(privateReceiptRecord)
	envelope, err := record.ConsumeTypedEnvelope(signed, destination)
	if err != nil {
		return SignedReceipt{}, ErrInvalid
	}
	if !bytes.Equal(envelope.PayloadType, []byte(privateReceiptCodec)) {
		return SignedReceipt{}, ErrInvalid
	}
	signer, err := peer.IDFromPublicKey(envelope.PublicKey)
	if err != nil || signer != server {
		return SignedReceipt{}, ErrWrongPeer
	}
	commitment := LocatorCommitment(locator)
	message := destination.message
	if message == nil || message.ProtocolVersion != ProtocolVersion || !bytes.Equal(message.LocatorCommitment, commitment[:]) {
		return SignedReceipt{}, ErrInvalid
	}
	value, err := receiptFromProto(message.Receipt)
	if err != nil {
		return SignedReceipt{}, err
	}
	if value.ServerPeer != server {
		return SignedReceipt{}, ErrWrongPeer
	}
	if err := validateReceipt(value, now); err != nil {
		return SignedReceipt{}, err
	}
	value.PrivateLocatorCommitment = commitment
	return SignedReceipt{Receipt: value, SignedEnvelope: append([]byte(nil), signed...), RawPayload: append([]byte(nil), envelope.RawPayload...)}, nil
}
