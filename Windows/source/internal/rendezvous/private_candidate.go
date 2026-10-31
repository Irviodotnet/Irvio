// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"time"

	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"
	"google.golang.org/protobuf/proto"
)

func PrivateCandidateToProto(candidate Candidate) (*irviov1.BrokeredCandidate, error) {
	if candidate.Descriptor.Announcement == nil || candidate.Descriptor.Announcement.Capabilities&protocol.CapPrivateExit == 0 {
		return nil, ErrInvalid
	}
	wire, err := candidateToProto(candidate)
	if err != nil {
		return nil, err
	}
	if proto.Size(wire) > PrivateMaxFrameSize-256 {
		return nil, ErrInvalid
	}
	return wire, nil
}

// PrivateCandidateFromProto verifies exact identity/locator and receipts; callers still authenticate eligible owner servers.
func PrivateCandidateFromProto(message *irviov1.BrokeredCandidate, target peer.ID, locator [32]byte, now time.Time) (Candidate, error) {
	if message == nil || target == "" || locator == ([32]byte{}) || proto.Size(message) > PrivateMaxFrameSize ||
		len(message.OwnerReceipts) == 0 || len(message.OwnerReceipts) > MaxOwnerRoutes ||
		len(message.OwnerRoutes) == 0 || len(message.OwnerRoutes) > MaxOwnerRoutes {
		return Candidate{}, ErrInvalid
	}
	descriptor, err := VerifyPrivateDescriptor(message.SignedAnnouncement, target, now)
	if err != nil {
		return Candidate{}, err
	}
	if !descriptor.Announcement.AcceptingNewSessions {
		return Candidate{}, ErrUnauthorized
	}
	candidate := Candidate{Descriptor: descriptor}
	for _, signed := range message.OwnerReceipts {
		if len(signed) == 0 || len(signed) > MaxReceiptSize {
			return Candidate{}, ErrInvalid
		}
		envelope, err := record.UnmarshalEnvelope(signed)
		if err != nil {
			return Candidate{}, ErrInvalid
		}
		server, err := peer.IDFromPublicKey(envelope.PublicKey)
		if err != nil {
			return Candidate{}, ErrInvalid
		}
		receipt, err := VerifyPrivateReceipt(signed, server, locator, now)
		if err != nil {
			return Candidate{}, err
		}
		candidate.Receipts = append(candidate.Receipts, receipt)
	}
	for _, wire := range message.OwnerRoutes {
		route, err := ownerRouteFromProto(wire)
		if err != nil {
			return Candidate{}, err
		}
		candidate.Routes = append(candidate.Routes, route)
	}
	if err := validateCandidate(candidate); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}
