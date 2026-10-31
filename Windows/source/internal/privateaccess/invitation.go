// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"
	"google.golang.org/protobuf/proto"
)

const InvitationPrefix = "irvio-private:v1:"
const MaxInvitationLinkBytes = 2048
const invitationDomain = "irvio/private-invitation/v1"
const invitationCodec = "\x84\x80\xc0\x01"

type InvitationToken struct {
	Host       peer.ID
	Locator    [32]byte
	Invitation Invitation
}

func (InvitationToken) String() string { return "[private invitation redacted]" }

type invitationRecord struct {
	message *irviov1.PrivateInvitationPayload
}

func (*invitationRecord) Domain() string { return invitationDomain }
func (*invitationRecord) Codec() []byte  { return []byte(invitationCodec) }
func (r *invitationRecord) MarshalRecord() ([]byte, error) {
	if r == nil || r.message == nil {
		return nil, ErrInvalid
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(r.message)
}
func (r *invitationRecord) UnmarshalRecord(data []byte) error {
	if len(data) == 0 || len(data) > MaxInvitationLinkBytes {
		return ErrInvalid
	}
	r.message = new(irviov1.PrivateInvitationPayload)
	if err := (proto.UnmarshalOptions{RecursionLimit: 4}).Unmarshal(data, r.message); err != nil {
		return ErrInvalid
	}
	canonical, err := r.MarshalRecord()
	if err != nil || len(r.message.ProtoReflect().GetUnknown()) != 0 || !bytes.Equal(data, canonical) {
		return ErrInvalid
	}
	return nil
}

func validateInvitationToken(value InvitationToken, now time.Time) error {
	i := value.Invitation
	if value.Host.Validate() != nil || value.Locator == ([32]byte{}) || !validHex(i.ID, 16) || !validHex(i.Secret, 32) || i.State != "open" || i.Client != "" || i.CreatedAt <= 0 || i.ExpiresAt <= i.CreatedAt || i.ExpiresAt-i.CreatedAt > int64(MaxInvitationLifetime/time.Second) || i.CreatedAt > now.Add(time.Minute).Unix() {
		return ErrInvalid
	}
	id, _ := hex.DecodeString(i.ID)
	secret, _ := hex.DecodeString(i.Secret)
	if bytes.Equal(id, make([]byte, 16)) || bytes.Equal(secret, make([]byte, 32)) {
		return ErrInvalid
	}
	if i.ExpiresAt <= now.Unix() {
		return ErrExpired
	}
	return nil
}

// EncodeInvitation signs an introduction, never an authorization grant.
func EncodeInvitation(key crypto.PrivKey, value InvitationToken, now time.Time) (string, error) {
	if key == nil || key.Type() != crypto.Ed25519 {
		return "", ErrInvalid
	}
	if err := validateInvitationToken(value, now); err != nil {
		return "", err
	}
	signer, err := peer.IDFromPrivateKey(key)
	if err != nil || signer != value.Host {
		return "", ErrDenied
	}
	id, _ := hex.DecodeString(value.Invitation.ID)
	secret, _ := hex.DecodeString(value.Invitation.Secret)
	envelope, err := record.Seal(&invitationRecord{message: &irviov1.PrivateInvitationPayload{ProtocolVersion: 1, HostPeerId: []byte(value.Host), Locator: value.Locator[:], InvitationId: id, EnrollmentSecret: secret, CreatedAtUnix: value.Invitation.CreatedAt, ExpiresAtUnix: value.Invitation.ExpiresAt}}, key)
	if err != nil {
		return "", ErrInvalid
	}
	signed, err := envelope.Marshal()
	if err != nil {
		return "", ErrInvalid
	}
	link := InvitationPrefix + base64.RawURLEncoding.EncodeToString(signed)
	if len(link) > MaxInvitationLinkBytes {
		return "", ErrInvalid
	}
	return link, nil
}

// DecodeInvitation verifies bounded canonical data without importing a profile or connecting.
func DecodeInvitation(link string, now time.Time) (InvitationToken, error) {
	return decodeInvitation(link, now, false)
}

func decodeInvitation(link string, now time.Time, allowExpired bool) (InvitationToken, error) {
	if len(link) > MaxInvitationLinkBytes || !strings.HasPrefix(link, InvitationPrefix) {
		return InvitationToken{}, ErrInvalid
	}
	encoded := strings.TrimPrefix(link, InvitationPrefix)
	signed, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawURLEncoding.EncodeToString(signed) != encoded {
		return InvitationToken{}, ErrInvalid
	}
	payload := new(invitationRecord)
	envelope, err := record.ConsumeTypedEnvelope(signed, payload)
	if err != nil || !bytes.Equal(envelope.PayloadType, []byte(invitationCodec)) || envelope.PublicKey.Type() != crypto.Ed25519 {
		return InvitationToken{}, ErrInvalid
	}
	message := payload.message
	if message == nil || message.ProtocolVersion != 1 || len(message.Locator) != 32 || len(message.InvitationId) != 16 || len(message.EnrollmentSecret) != 32 {
		return InvitationToken{}, ErrInvalid
	}
	host, err := peer.IDFromBytes(message.HostPeerId)
	if err != nil {
		return InvitationToken{}, ErrInvalid
	}
	signer, err := peer.IDFromPublicKey(envelope.PublicKey)
	if err != nil || signer != host {
		return InvitationToken{}, ErrDenied
	}
	value := InvitationToken{Host: host, Invitation: Invitation{ID: hex.EncodeToString(message.InvitationId), Secret: hex.EncodeToString(message.EnrollmentSecret), CreatedAt: message.CreatedAtUnix, ExpiresAt: message.ExpiresAtUnix, State: "open"}}
	copy(value.Locator[:], message.Locator)
	if err := validateInvitationToken(value, now); err != nil && !(allowExpired && err == ErrExpired) {
		return InvitationToken{}, err
	}
	return value, nil
}
