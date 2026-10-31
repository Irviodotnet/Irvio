// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"crypto/subtle"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func (s *Store) SaveTarget(target Target) error {
	return s.update(func(state *State) error {
		for i := range state.Targets {
			if state.Targets[i].PeerID == target.PeerID {
				state.Targets[i] = target
				return nil
			}
		}
		state.Targets = append(state.Targets, target)
		return nil
	})
}

func (s *Store) RemoveTarget(id peer.ID) error {
	return s.update(func(state *State) error {
		for i := range state.Targets {
			if state.Targets[i].PeerID == id.String() {
				state.Targets = append(state.Targets[:i], state.Targets[i+1:]...)
				break
			}
		}
		return nil
	})
}

func (s *Store) CreateInvitation(now time.Time, lifetime time.Duration) (Invitation, error) {
	return s.createInvitation(now, lifetime, false)
}

// CreateAccessInvitation grants one authenticated redeemer access without a later approval.
func (s *Store) CreateAccessInvitation(now time.Time, lifetime time.Duration) (Invitation, error) {
	return s.createInvitation(now, lifetime, true)
}

func (s *Store) createInvitation(now time.Time, lifetime time.Duration, automatic bool) (Invitation, error) {
	if lifetime < time.Second || lifetime > MaxInvitationLifetime {
		return Invitation{}, ErrInvalid
	}
	id, err := randomHex(16)
	if err != nil {
		return Invitation{}, err
	}
	secret, err := randomHex(32)
	if err != nil {
		return Invitation{}, err
	}
	invite := Invitation{ID: id, Secret: secret, CreatedAt: now.Unix(), ExpiresAt: now.Add(lifetime).Unix(), State: "open"}
	invite.AutoApprove = automatic
	err = s.update(func(state *State) error { state.Invitations = append(state.Invitations, invite); return nil })
	if err != nil {
		return Invitation{}, err
	}
	return invite, nil
}

// Reserve accepts only an end-client identity authenticated by the enrollment transport.
func (s *Store) Reserve(id, secret string, client peer.ID, now time.Time) error {
	if !validPeer(client.String()) || !validHex(secret, 32) {
		return ErrDenied
	}
	return s.update(func(state *State) error {
		if client.String() == state.HostPeerID {
			return ErrDenied
		}
		for i := range state.Invitations {
			invite := &state.Invitations[i]
			if invite.ID != id {
				continue
			}
			if invite.State == "consumed" || subtle.ConstantTimeCompare([]byte(invite.Secret), []byte(secret)) != 1 {
				return ErrDenied
			}
			if now.Unix() < invite.CreatedAt || now.Unix() >= invite.ExpiresAt {
				return ErrExpired
			}
			if invite.Client != "" && invite.Client != client.String() {
				return ErrDenied
			}
			invite.Client, invite.State = client.String(), "pending"
			if invite.AutoApprove {
				grantInvitation(state, invite, "")
			}
			return nil
		}
		return ErrDenied
	})
}

func (s *Store) Approve(id, name string, now time.Time) error {
	return s.update(func(state *State) error {
		for i := range state.Invitations {
			invite := &state.Invitations[i]
			if invite.ID != id {
				continue
			}
			if invite.State != "pending" {
				return ErrDenied
			}
			if now.Unix() < invite.CreatedAt || now.Unix() >= invite.ExpiresAt {
				return ErrExpired
			}
			grantInvitation(state, invite, name)
			return nil
		}
		return ErrDenied
	})
}

func grantInvitation(state *State, invite *Invitation, name string) {
	member := Member{PeerID: invite.Client, Name: name, Allowed: true, Revision: state.Revision}
	found := false
	for j := range state.Members {
		if state.Members[j].PeerID == invite.Client {
			if state.Members[j].Allowed {
				member.Revision = state.Members[j].Revision
			}
			state.Members[j] = member
			found = true
			break
		}
	}
	if !found {
		state.Members = append(state.Members, member)
	}
	invite.State, invite.Secret = "consumed", ""
}

func (s *Store) CancelInvitation(id string) error {
	return s.update(func(state *State) error {
		for i := range state.Invitations {
			if state.Invitations[i].ID == id {
				state.Invitations[i].State, state.Invitations[i].Secret = "consumed", ""
				return nil
			}
		}
		return ErrDenied
	})
}

// Revoke persists denial; the runtime must additionally fence admissions and revoke resources.
func (s *Store) Revoke(client peer.ID) error {
	return s.update(func(state *State) error {
		found := false
		for i := range state.Invitations {
			if state.Invitations[i].Client == client.String() {
				state.Invitations[i].State, state.Invitations[i].Secret = "consumed", ""
				found = true
			}
		}
		for i := range state.Members {
			if state.Members[i].PeerID == client.String() {
				state.Members[i].Allowed = false
				state.Members[i].Revision = state.Revision
				return nil
			}
		}
		if found {
			return nil
		}
		return ErrDenied
	})
}

func (s *Store) PruneInvitations(now time.Time) error {
	return s.update(func(state *State) error {
		kept := state.Invitations[:0]
		for _, invite := range state.Invitations {
			if invite.State != "consumed" && invite.ExpiresAt > now.Unix() {
				kept = append(kept, invite)
			}
		}
		state.Invitations = kept
		return nil
	})
}
