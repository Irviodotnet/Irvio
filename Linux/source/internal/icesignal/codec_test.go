// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	"github.com/Irviodotnet/Irvio/internal/icesession"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func TestOfferAnswerCodecRoundTrip(t *testing.T) {
	initiator := testPeerID(t)
	responder := testPeerID(t)
	offer := testOffer(t, initiator, responder)
	wire := offerToProto(offer)
	decoded, err := offerFromProto(wire, testValidation())
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SessionID != offer.SessionID || decoded.Initiator != initiator ||
		decoded.Responder != responder || !equalDescription(decoded.Description, offer.Description) {
		t.Fatalf("offer round trip mismatch: %+v", decoded)
	}
	answer := Answer{
		SessionID: offer.SessionID, Initiator: initiator, Responder: responder,
		Purpose: icesession.PurposeExit, Role: icesession.RoleControlled,
		Description: offer.Description.Clone(), CreatedAt: offer.CreatedAt, ExpiresAt: offer.ExpiresAt,
	}
	decodedAnswer, err := answerFromProto(answerToProto(answer), testValidation())
	if err != nil {
		t.Fatal(err)
	}
	if decodedAnswer.Role != icesession.RoleControlled || !equalDescription(decodedAnswer.Description, answer.Description) {
		t.Fatalf("answer round trip mismatch: %+v", decodedAnswer)
	}
}

func TestFrameBoundsAndShortWrites(t *testing.T) {
	message := offerToProto(testOffer(t, testPeerID(t), testPeerID(t)))
	var short shortWriter
	if err := writeFrame(&short, message); err != nil {
		t.Fatal(err)
	}
	decoded := new(irviov1.IceOffer)
	if err := readFrame(bytes.NewReader(short.Bytes()), decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(message, decoded) {
		t.Fatal("short-write frame changed message")
	}
	for name, frame := range map[string][]byte{
		"missing header": nil,
		"zero":           {0, 0, 0, 0},
		"oversized":      frameHeader(MaxFrameSize + 1),
		"truncated":      append(frameHeader(10), 1, 2),
		"malformed":      append(frameHeader(1), 0xff),
	} {
		t.Run(name, func(t *testing.T) {
			if err := readFrame(bytes.NewReader(frame), new(irviov1.IceOffer)); err == nil {
				t.Fatal("invalid frame accepted")
			}
		})
	}
}

func TestDescriptionWireValidation(t *testing.T) {
	base := descriptionToProto(testDescription())
	cases := map[string]func(*irviov1.IceDescription){
		"unknown type": func(value *irviov1.IceDescription) {
			value.Candidates[0].Type = irviov1.IceCandidateType(99)
		},
		"unknown network": func(value *irviov1.IceDescription) {
			value.Candidates[0].Network = irviov1.IceNetworkType(99)
		},
		"invalid address": func(value *irviov1.IceDescription) {
			value.Candidates[0].Address = []byte{1, 2, 3}
		},
		"mapped address": func(value *irviov1.IceDescription) {
			value.Candidates[0].Network = irviov1.IceNetworkType_ICE_NETWORK_TYPE_UDP6
			value.Candidates[0].Address = netip.MustParseAddr("::ffff:192.0.2.1").AsSlice()
		},
		"zero port": func(value *irviov1.IceDescription) {
			value.Candidates[0].Port = 0
		},
		"large port": func(value *irviov1.IceDescription) {
			value.Candidates[0].Port = 65536
		},
		"relay type": func(value *irviov1.IceDescription) {
			value.Candidates[0].Type = irviov1.IceCandidateType(4)
		},
		"tcp network": func(value *irviov1.IceDescription) {
			value.Candidates[0].Network = irviov1.IceNetworkType(3)
		},
		"oversized foundation": func(value *irviov1.IceDescription) {
			value.Candidates[0].Foundation = strings.Repeat("f", 65)
		},
		"oversized ufrag": func(value *irviov1.IceDescription) {
			value.Credentials.Ufrag = strings.Repeat("u", 257)
		},
		"oversized password": func(value *irviov1.IceDescription) {
			value.Credentials.Pwd = strings.Repeat("p", 257)
		},
		"duplicate": func(value *irviov1.IceDescription) {
			value.Candidates = append(value.Candidates, proto.Clone(value.Candidates[0]).(*irviov1.IceCandidate))
		},
		"too many": func(value *irviov1.IceDescription) {
			for len(value.Candidates) <= testValidation().MaxCandidates {
				value.Candidates = append(value.Candidates, proto.Clone(value.Candidates[0]).(*irviov1.IceCandidate))
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := proto.Clone(base).(*irviov1.IceDescription)
			mutate(value)
			if _, err := descriptionFromProto(value, testValidation()); err == nil {
				t.Fatal("invalid description accepted")
			}
		})
	}
}

func TestOfferIdentityRoleAndExpiryValidation(t *testing.T) {
	initiator := testPeerID(t)
	responder := testPeerID(t)
	other := testPeerID(t)
	config := testConfig()
	base := testOffer(t, initiator, responder)
	if err := validateOffer(base, initiator, responder, time.Now(), config); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Offer){
		"forged initiator": func(value *Offer) { value.Initiator = other },
		"wrong responder":  func(value *Offer) { value.Responder = other },
		"self session":     func(value *Offer) { value.Responder = value.Initiator },
		"wrong role":       func(value *Offer) { value.Role = icesession.RoleControlled },
		"wrong purpose":    func(value *Offer) { value.Purpose = 0 },
		"expired": func(value *Offer) {
			value.CreatedAt = time.Now().Add(-2 * time.Minute)
			value.ExpiresAt = time.Now().Add(-time.Minute)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			value := base
			mutate(&value)
			if err := validateOffer(value, initiator, responder, time.Now(), config); err == nil {
				t.Fatal("invalid offer accepted")
			}
		})
	}
}

func FuzzIceOfferFrame(f *testing.F) {
	valid := offerToProto(testOffer(f, testPeerID(f), testPeerID(f)))
	var encoded bytes.Buffer
	if err := writeFrame(&encoded, valid); err != nil {
		f.Fatal(err)
	}
	f.Add(encoded.Bytes())
	f.Add([]byte{0, 0, 0, 1, 0xff})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxFrameSize+4 {
			input = input[:MaxFrameSize+4]
		}
		wire := new(irviov1.IceOffer)
		if readFrame(bytes.NewReader(input), wire) == nil {
			_, _ = offerFromProto(wire, testValidation())
		}
	})
}

func FuzzIceAnswerFrame(f *testing.F) {
	offer := testOffer(f, testPeerID(f), testPeerID(f))
	answer := Answer{
		SessionID: offer.SessionID, Initiator: offer.Initiator, Responder: offer.Responder,
		Purpose: offer.Purpose, Role: icesession.RoleControlled,
		Description: offer.Description, CreatedAt: offer.CreatedAt, ExpiresAt: offer.ExpiresAt,
	}
	response := &irviov1.IceSignalResponse{Result: &irviov1.IceSignalResponse_Answer{Answer: answerToProto(answer)}}
	var encoded bytes.Buffer
	if err := writeFrame(&encoded, response); err != nil {
		f.Fatal(err)
	}
	f.Add(encoded.Bytes())
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > MaxFrameSize+4 {
			input = input[:MaxFrameSize+4]
		}
		wire := new(irviov1.IceSignalResponse)
		if readFrame(bytes.NewReader(input), wire) == nil {
			if answer := wire.GetAnswer(); answer != nil {
				_, _ = answerFromProto(answer, testValidation())
			}
		}
	})
}

func FuzzIceCandidateValidation(f *testing.F) {
	f.Add([]byte{127, 0, 0, 1}, uint32(41000), uint32(1), "foundation")
	f.Add([]byte{0xff}, uint32(0), uint32(99), "")
	f.Fuzz(func(t *testing.T, address []byte, port, candidateType uint32, foundation string) {
		if len(address) > 32 {
			address = address[:32]
		}
		if len(foundation) > 128 {
			foundation = foundation[:128]
		}
		wire := descriptionToProto(testDescription())
		wire.Candidates[0].Address = append([]byte(nil), address...)
		wire.Candidates[0].Port = port
		wire.Candidates[0].Type = irviov1.IceCandidateType(candidateType)
		wire.Candidates[0].Foundation = foundation
		description, err := descriptionFromProto(wire, testValidation())
		if err == nil {
			_, _ = icesession.CandidateToPion(description.Candidates[0], testValidation())
		}
	})
}

type shortWriter struct {
	bytes.Buffer
}

func (w *shortWriter) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	return w.Buffer.Write(payload[:1])
}

func frameHeader(length int) []byte {
	result := make([]byte, 4)
	binary.BigEndian.PutUint32(result, uint32(length))
	return result
}

func testPeerID(tb testing.TB) peer.ID {
	tb.Helper()
	privateKey, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		tb.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		tb.Fatal(err)
	}
	return id
}

func testValidation() icesession.ValidationConfig {
	result := icesession.DefaultValidationConfig()
	result.AllowLoopback = true
	return result
}

func testConfig() Config {
	result := DefaultConfig()
	result.Validation = testValidation()
	result.StreamTimeout = time.Second
	return result
}

func testDescription() icesession.Description {
	return icesession.Description{
		Credentials: icesession.Credentials{Ufrag: "localUfrag", Pwd: "abcdefghijklmnopqrstuv"},
		Candidates: []icesession.Candidate{{
			Type: icesession.CandidateTypeHost, Network: icesession.NetworkUDP4,
			Address: netip.MustParseAddr("127.0.0.1"), Port: 41000,
			Foundation: "foundation", Priority: 2130706431, Component: 1,
		}},
	}
}

func testOffer(tb testing.TB, initiator, responder peer.ID) Offer {
	tb.Helper()
	id, err := icesession.NewSessionID()
	if err != nil {
		tb.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	return Offer{
		SessionID: id, Initiator: initiator, Responder: responder,
		Purpose: icesession.PurposeExit, Role: icesession.RoleControlling,
		Description: testDescription(), CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
}

func equalDescription(left, right icesession.Description) bool {
	if left.Credentials != right.Credentials || len(left.Candidates) != len(right.Candidates) {
		return false
	}
	for index := range left.Candidates {
		if left.Candidates[index] != right.Candidates[index] {
			return false
		}
	}
	return true
}

var _ io.Writer = (*shortWriter)(nil)

func TestWireProtocolVersionIsIndependentAndStrict(t *testing.T) {
	offer := offerToProto(testOffer(t, testPeerID(t), testPeerID(t)))
	if err := validateOfferWireVersion(offer); err != nil {
		t.Fatal(err)
	}
	offer.ProtocolVersion = ProtocolVersion + 1
	if err := validateOfferWireVersion(offer); err == nil {
		t.Fatal("unsupported Offer version accepted")
	}
	answer := &irviov1.IceAnswer{ProtocolVersion: ProtocolVersion}
	if err := validateAnswerWireVersion(answer); err != nil {
		t.Fatal(err)
	}
	answer.ProtocolVersion = 0
	if err := validateAnswerWireVersion(answer); err == nil {
		t.Fatal("unspecified Answer version accepted")
	}
}
