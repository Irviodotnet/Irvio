// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"
)

func TestInvitationAuthenticatedRoundtripAndBounds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	token := InvitationToken{Host: host, Locator: [32]byte{1}, Invitation: Invitation{ID: strings.Repeat("12", 16), Secret: strings.Repeat("34", 32), CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(), State: "open"}}
	link, err := EncodeInvitation(key, token, now)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeInvitation(link, now)
	if err != nil || decoded != token {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%v", decoded), token.Invitation.Secret) {
		t.Fatal("default formatting leaked invitation secret")
	}
	if _, err := DecodeInvitation(link, now.Add(time.Hour)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry boundary: %v", err)
	}
	for _, bad := range []string{"", "irvio-bootstrap:v1:" + strings.TrimPrefix(link, InvitationPrefix), link + "=", link + "\n", strings.Repeat("x", MaxInvitationLinkBytes+1)} {
		if _, err := DecodeInvitation(bad, now); err == nil {
			t.Fatal("invalid encoding accepted")
		}
	}
	signed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, InvitationPrefix))
	if err != nil {
		t.Fatal(err)
	}
	signed[len(signed)-1] ^= 1
	if _, err := DecodeInvitation(InvitationPrefix+base64.RawURLEncoding.EncodeToString(signed), now); err == nil {
		t.Fatal("tampered signature accepted")
	}
	other, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeInvitation(other, token, now); !errors.Is(err, ErrDenied) {
		t.Fatal("wrong host signed invitation")
	}
	for _, change := range []func(*InvitationToken){
		func(v *InvitationToken) { v.Locator = [32]byte{} },
		func(v *InvitationToken) { v.Invitation.Secret = strings.Repeat("00", 32) },
		func(v *InvitationToken) { v.Invitation.ID = strings.Repeat("00", 16) },
		func(v *InvitationToken) { v.Invitation.ExpiresAt = now.Add(25 * time.Hour).Unix() },
		func(v *InvitationToken) { v.Invitation.CreatedAt = now.Add(2 * time.Minute).Unix() },
		func(v *InvitationToken) { v.Invitation.State = "consumed" },
	} {
		bad := token
		change(&bad)
		if _, err := EncodeInvitation(key, bad, now); err == nil {
			t.Fatal("invalid invitation signed")
		}
	}
}

func TestInvitationRejectsAuthenticatedMalformedPayload(t *testing.T) {
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []*irviov1.PrivateInvitationPayload{{ProtocolVersion: 2}, {ProtocolVersion: 1, Locator: []byte{1}}} {
		envelope, err := record.Seal(&invitationRecord{message: message}, key)
		if err != nil {
			t.Fatal(err)
		}
		signed, err := envelope.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeInvitation(InvitationPrefix+base64.RawURLEncoding.EncodeToString(signed), time.Now()); err == nil {
			t.Fatal("signature bypassed payload validation")
		}
	}
	r := new(invitationRecord)
	for _, data := range [][]byte{{8, 1, 8, 1}, {8, 1, 0x78, 1}, {8, 0x81, 0}, {}} {
		if err := r.UnmarshalRecord(data); err == nil {
			t.Fatal("noncanonical or unknown payload accepted")
		}
	}
}
