// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import (
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"
)

// ConnectivityBinding ties a prepared session to authenticated signaling without exposing credentials.
type ConnectivityBinding struct {
	SessionID  SessionID
	Initiator  peer.ID
	Responder  peer.ID
	LocalPeer  peer.ID
	RemotePeer peer.ID
	Purpose    SessionPurpose
	LocalRole  Role
	RemoteRole Role
}

// Validate checks the fixed Irvio v1 initiator, responder, and role mapping.
func (b ConnectivityBinding) Validate() error {
	if b.SessionID == (SessionID{}) || b.Purpose != PurposeExit {
		return fmt.Errorf("invalid ICE connectivity binding metadata")
	}
	for _, id := range []peer.ID{b.Initiator, b.Responder, b.LocalPeer, b.RemotePeer} {
		if id == "" || id.Validate() != nil {
			return fmt.Errorf("invalid ICE connectivity binding PeerID")
		}
	}
	if b.Initiator == b.Responder || b.LocalPeer == b.RemotePeer {
		return fmt.Errorf("invalid ICE connectivity peer relationship")
	}
	if b.LocalPeer == b.Initiator && b.RemotePeer == b.Responder &&
		b.LocalRole == RoleControlling && b.RemoteRole == RoleControlled {
		return nil
	}
	if b.LocalPeer == b.Responder && b.RemotePeer == b.Initiator &&
		b.LocalRole == RoleControlled && b.RemoteRole == RoleControlling {
		return nil
	}
	return fmt.Errorf("invalid ICE connectivity role mapping")
}
