// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessionestablish

import (
	"bytes"
	"time"

	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/peer"
)

func validPrepareTarget(payload *irviov1.SessionEstablishPayload, prepare *irviov1.SessionPrepare) bool {
	if payload.ProtocolVersion == ProtocolVersion {
		return validCountry(prepare.CountryIntent) && len(prepare.PrivateTargetPeerId) == 0
	}
	return payload.ProtocolVersion == PrivateProtocolVersion && prepare.CountryIntent == "" && bytes.Equal(prepare.PrivateTargetPeerId, payload.RecipientPeerId)
}

func privateDescriptorBound(descriptor DescriptorBinding, target peer.ID, now time.Time) bool {
	verified, err := protocol.VerifySignedAnnouncementRecord(descriptor.SignedEnvelope, target, now)
	return err == nil && verified.Announcement.Capabilities&protocol.CapPrivateExit != 0 && verified.Announcement.Capabilities&protocol.CapExit == 0 &&
		verified.Announcement.AcceptingNewSessions && verified.DescriptorID == descriptor.ID &&
		verified.Announcement.Incarnation == descriptor.Incarnation && verified.Announcement.Sequence == descriptor.Sequence &&
		verified.Announcement.AwgKeyEpoch == descriptor.AWGKeyEpoch && bytes.Equal(verified.Announcement.AwgPublicKey, descriptor.AWGPublicKey[:])
}
