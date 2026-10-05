// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	"hydra.local/hydra/internal/icesession"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

func writeFrame(writer io.Writer, message proto.Message) error {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal ICE signaling frame: %w", err)
	}
	if len(payload) == 0 || len(payload) > MaxFrameSize {
		return fmt.Errorf("%w: frame size %d", ErrInvalidMessage, len(payload))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return fmt.Errorf("write ICE signaling frame length: %w", err)
	}
	if err := writeAll(writer, payload); err != nil {
		return fmt.Errorf("write ICE signaling frame: %w", err)
	}
	return nil
}
func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if written < 0 || written > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[written:]
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func readFrame(reader io.Reader, target proto.Message) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return fmt.Errorf("read ICE signaling frame length: %w", err)
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > MaxFrameSize {
		return fmt.Errorf("%w: frame size %d", ErrInvalidMessage, length)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("read ICE signaling frame: %w", err)
	}
	if err := (proto.UnmarshalOptions{RecursionLimit: 32}).Unmarshal(payload, target); err != nil {
		return fmt.Errorf("%w: malformed protobuf", ErrInvalidMessage)
	}
	return nil
}

func offerToProto(offer Offer) *irviov1.IceOffer {
	return &irviov1.IceOffer{
		ProtocolVersion: ProtocolVersion,
		SessionId:       append([]byte(nil), offer.SessionID[:]...),
		InitiatorPeerId: []byte(offer.Initiator), ResponderPeerId: []byte(offer.Responder),
		Purpose: purposeToProto(offer.Purpose), Role: roleToProto(offer.Role),
		Description:   descriptionToProto(offer.Description),
		CreatedAtUnix: offer.CreatedAt.Unix(), ExpiresAtUnix: offer.ExpiresAt.Unix(),
	}
}

// OfferToProto encodes bounded offers for authenticated direct and brokered M12 carriers.
func OfferToProto(offer Offer) *irviov1.IceOffer {
	return offerToProto(offer)
}

func answerToProto(answer Answer) *irviov1.IceAnswer {
	return &irviov1.IceAnswer{
		ProtocolVersion: ProtocolVersion,
		SessionId:       append([]byte(nil), answer.SessionID[:]...),
		InitiatorPeerId: []byte(answer.Initiator), ResponderPeerId: []byte(answer.Responder),
		Purpose: purposeToProto(answer.Purpose), Role: roleToProto(answer.Role),
		Description:   descriptionToProto(answer.Description),
		CreatedAtUnix: answer.CreatedAt.Unix(), ExpiresAtUnix: answer.ExpiresAt.Unix(),
	}
}

// AnswerToProto encodes bounded answers for authenticated direct and brokered M12 carriers.
func AnswerToProto(answer Answer) *irviov1.IceAnswer {
	return answerToProto(answer)
}

func offerFromProto(message *irviov1.IceOffer, validation icesession.ValidationConfig) (Offer, error) {
	if message == nil {
		return Offer{}, ErrInvalidMessage
	}
	id, initiator, responder, description, err := commonFromProto(
		message.SessionId, message.InitiatorPeerId, message.ResponderPeerId, message.Description, validation,
	)
	if err != nil {
		return Offer{}, err
	}
	return Offer{
		SessionID: id, Initiator: initiator, Responder: responder,
		Purpose: purposeFromProto(message.Purpose), Role: roleFromProto(message.Role),
		Description: description, CreatedAt: time.Unix(message.CreatedAtUnix, 0),
		ExpiresAt: time.Unix(message.ExpiresAtUnix, 0),
	}, nil
}

// OfferFromProto retains legacy authenticated ICE description validation.
func OfferFromProto(message *irviov1.IceOffer, validation icesession.ValidationConfig) (Offer, error) {
	return offerFromProto(message, validation)
}

func answerFromProto(message *irviov1.IceAnswer, validation icesession.ValidationConfig) (Answer, error) {
	if message == nil {
		return Answer{}, ErrInvalidMessage
	}
	id, initiator, responder, description, err := commonFromProto(
		message.SessionId, message.InitiatorPeerId, message.ResponderPeerId, message.Description, validation,
	)
	if err != nil {
		return Answer{}, err
	}
	return Answer{
		SessionID: id, Initiator: initiator, Responder: responder,
		Purpose: purposeFromProto(message.Purpose), Role: roleFromProto(message.Role),
		Description: description, CreatedAt: time.Unix(message.CreatedAtUnix, 0),
		ExpiresAt: time.Unix(message.ExpiresAtUnix, 0),
	}, nil
}

// AnswerFromProto retains legacy authenticated ICE description validation.
func AnswerFromProto(message *irviov1.IceAnswer, validation icesession.ValidationConfig) (Answer, error) {
	return answerFromProto(message, validation)
}

func commonFromProto(sessionBytes, initiatorBytes, responderBytes []byte, wire *irviov1.IceDescription, validation icesession.ValidationConfig) (icesession.SessionID, peer.ID, peer.ID, icesession.Description, error) {
	if len(sessionBytes) != icesession.SessionIDSize {
		return icesession.SessionID{}, "", "", icesession.Description{}, fmt.Errorf("%w: invalid session ID", ErrInvalidMessage)
	}
	var id icesession.SessionID
	copy(id[:], sessionBytes)
	if id == (icesession.SessionID{}) {
		return id, "", "", icesession.Description{}, fmt.Errorf("%w: zero session ID", ErrInvalidMessage)
	}
	initiator, err := peer.IDFromBytes(initiatorBytes)
	if err != nil {
		return id, "", "", icesession.Description{}, fmt.Errorf("%w: invalid initiator", ErrInvalidMessage)
	}
	responder, err := peer.IDFromBytes(responderBytes)
	if err != nil {
		return id, "", "", icesession.Description{}, fmt.Errorf("%w: invalid responder", ErrInvalidMessage)
	}
	description, err := descriptionFromProto(wire, validation)
	return id, initiator, responder, description, err
}

func descriptionToProto(description icesession.Description) *irviov1.IceDescription {
	result := &irviov1.IceDescription{Credentials: &irviov1.IceCredentials{
		Ufrag: description.Credentials.Ufrag, Pwd: description.Credentials.Pwd,
	}}
	result.Candidates = make([]*irviov1.IceCandidate, 0, len(description.Candidates))
	for _, candidate := range description.Candidates {
		wire := &irviov1.IceCandidate{
			Type: candidateTypeToProto(candidate.Type), Network: networkToProto(candidate.Network),
			Address: addressBytes(candidate.Address), Port: uint32(candidate.Port),
			Foundation: candidate.Foundation, Priority: candidate.Priority, Component: uint32(candidate.Component),
		}
		if candidate.RelatedAddress.IsValid() {
			wire.RelatedAddress = addressBytes(candidate.RelatedAddress)
			wire.RelatedPort = uint32(candidate.RelatedPort)
		}
		result.Candidates = append(result.Candidates, wire)
	}
	return result
}

func descriptionFromProto(message *irviov1.IceDescription, validation icesession.ValidationConfig) (icesession.Description, error) {
	if message == nil || message.Credentials == nil || len(message.Candidates) > validation.MaxCandidates {
		return icesession.Description{}, fmt.Errorf("%w: missing or excessive ICE description", ErrInvalidMessage)
	}
	result := icesession.Description{Credentials: icesession.Credentials{
		Ufrag: message.Credentials.Ufrag, Pwd: message.Credentials.Pwd,
	}, Candidates: make([]icesession.Candidate, 0, len(message.Candidates))}
	for _, wire := range message.Candidates {
		if wire == nil || wire.Port == 0 || wire.Port > 65535 || wire.RelatedPort > 65535 || wire.Component > 65535 {
			return icesession.Description{}, fmt.Errorf("%w: invalid candidate integers", ErrInvalidMessage)
		}
		address, err := addressFromBytes(wire.Address)
		if err != nil {
			return icesession.Description{}, err
		}
		candidate := icesession.Candidate{
			Type: candidateTypeFromProto(wire.Type), Network: networkFromProto(wire.Network),
			Address: address, Port: uint16(wire.Port), Foundation: wire.Foundation,
			Priority: wire.Priority, Component: uint16(wire.Component),
		}
		if len(wire.RelatedAddress) != 0 {
			candidate.RelatedAddress, err = addressFromBytes(wire.RelatedAddress)
			if err != nil {
				return icesession.Description{}, err
			}
			candidate.RelatedPort = uint16(wire.RelatedPort)
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	validated, err := icesession.ValidateDescription(result, validation)
	if err != nil {
		return icesession.Description{}, fmt.Errorf("%w: invalid description", ErrInvalidMessage)
	}
	return validated, nil
}

func addressBytes(address netip.Addr) []byte {
	if address.Is4() {
		value := address.As4()
		return append([]byte(nil), value[:]...)
	}
	value := address.As16()
	return append([]byte(nil), value[:]...)
}

func addressFromBytes(value []byte) (netip.Addr, error) {
	var address netip.Addr
	switch len(value) {
	case 4:
		var raw [4]byte
		copy(raw[:], value)
		address = netip.AddrFrom4(raw)
	case 16:
		var raw [16]byte
		copy(raw[:], value)
		address = netip.AddrFrom16(raw)
	default:
		return netip.Addr{}, fmt.Errorf("%w: candidate address length %d", ErrInvalidMessage, len(value))
	}
	if address.Is4In6() {
		return netip.Addr{}, fmt.Errorf("%w: IPv4-mapped IPv6 address", ErrInvalidMessage)
	}
	return address, nil
}

func candidateTypeToProto(value icesession.CandidateType) irviov1.IceCandidateType {
	if value == icesession.CandidateTypeHost {
		return irviov1.IceCandidateType_ICE_CANDIDATE_TYPE_HOST
	}
	if value == icesession.CandidateTypeServerReflexive {
		return irviov1.IceCandidateType_ICE_CANDIDATE_TYPE_SERVER_REFLEXIVE
	}
	return irviov1.IceCandidateType_ICE_CANDIDATE_TYPE_UNSPECIFIED
}
func candidateTypeFromProto(value irviov1.IceCandidateType) icesession.CandidateType {
	if value == irviov1.IceCandidateType_ICE_CANDIDATE_TYPE_HOST {
		return icesession.CandidateTypeHost
	}
	if value == irviov1.IceCandidateType_ICE_CANDIDATE_TYPE_SERVER_REFLEXIVE {
		return icesession.CandidateTypeServerReflexive
	}
	return 0
}
func networkToProto(value icesession.NetworkType) irviov1.IceNetworkType {
	if value == icesession.NetworkUDP4 {
		return irviov1.IceNetworkType_ICE_NETWORK_TYPE_UDP4
	}
	if value == icesession.NetworkUDP6 {
		return irviov1.IceNetworkType_ICE_NETWORK_TYPE_UDP6
	}
	return irviov1.IceNetworkType_ICE_NETWORK_TYPE_UNSPECIFIED
}
func networkFromProto(value irviov1.IceNetworkType) icesession.NetworkType {
	if value == irviov1.IceNetworkType_ICE_NETWORK_TYPE_UDP4 {
		return icesession.NetworkUDP4
	}
	if value == irviov1.IceNetworkType_ICE_NETWORK_TYPE_UDP6 {
		return icesession.NetworkUDP6
	}
	return 0
}
func purposeToProto(value icesession.SessionPurpose) irviov1.IceSessionPurpose {
	if value == icesession.PurposeExit {
		return irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT
	}
	return irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_UNSPECIFIED
}
func purposeFromProto(value irviov1.IceSessionPurpose) icesession.SessionPurpose {
	if value == irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT {
		return icesession.PurposeExit
	}
	return 0
}
func roleToProto(value icesession.Role) irviov1.IceRole {
	if value == icesession.RoleControlling {
		return irviov1.IceRole_ICE_ROLE_CONTROLLING
	}
	if value == icesession.RoleControlled {
		return irviov1.IceRole_ICE_ROLE_CONTROLLED
	}
	return irviov1.IceRole_ICE_ROLE_UNSPECIFIED
}
func roleFromProto(value irviov1.IceRole) icesession.Role {
	if value == irviov1.IceRole_ICE_ROLE_CONTROLLING {
		return icesession.RoleControlling
	}
	if value == irviov1.IceRole_ICE_ROLE_CONTROLLED {
		return icesession.RoleControlled
	}
	return 0
}
