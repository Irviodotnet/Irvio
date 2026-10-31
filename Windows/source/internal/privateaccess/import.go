// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"context"
	"encoding/hex"
	"net"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ImportInvitation stores an introduction without granting access or creating a connectable target.
func (s *Store) ImportInvitation(link, name string, now time.Time) error {
	token, err := DecodeInvitation(link, now)
	if err != nil {
		return err
	}
	if !validName(name) {
		return ErrInvalid
	}
	return s.update(func(state *State) error {
		if token.Host.String() == state.HostPeerID {
			return ErrDenied
		}
		for _, existing := range state.Enrollments {
			old, err := decodeInvitation(existing.Link, now, true)
			if err != nil {
				return ErrInvalid
			}
			if old.Host == token.Host {
				if existing.Link == link {
					return nil
				}
				return ErrDenied
			}
		}
		state.Version = 2
		state.Enrollments = append(state.Enrollments, ImportedInvitation{Name: name, Link: link, State: "imported", Revision: state.Revision})
		return nil
	})
}

func (s *Store) CancelEnrollment(host peer.ID) error {
	return s.update(func(state *State) error {
		for i, entry := range state.Enrollments {
			token, err := decodeInvitation(entry.Link, time.Now(), true)
			if err != nil {
				return ErrInvalid
			}
			if token.Host == host {
				state.Enrollments = append(state.Enrollments[:i], state.Enrollments[i+1:]...)
				return nil
			}
		}
		return ErrDenied
	})
}

type EnrollmentDialer func(context.Context, InvitationToken) (net.Conn, error)

// ResumeEnrollment trusts only the pinned host's inner TLS response, never the routing carrier.
func (s *Store) ResumeEnrollment(ctx context.Context, host peer.ID, key crypto.PrivKey, dial EnrollmentDialer) (irviov1.PrivateEnrollmentResponse_Status, error) {
	denied := irviov1.PrivateEnrollmentResponse_DENIED
	if ctx == nil || key == nil || dial == nil {
		return denied, ErrInvalid
	}
	state, err := s.Snapshot()
	if err != nil {
		return denied, err
	}
	client, err := peer.IDFromPrivateKey(key)
	if err != nil || client.String() != state.HostPeerID {
		return denied, ErrDenied
	}
	var entry ImportedInvitation
	var token InvitationToken
	for _, candidate := range state.Enrollments {
		parsed, err := decodeInvitation(candidate.Link, time.Now(), true)
		if err != nil {
			return denied, ErrInvalid
		}
		if parsed.Host == host {
			entry, token = candidate, parsed
			break
		}
	}
	if entry.Link == "" {
		return denied, ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	exchange := func(statusOnly bool) (irviov1.PrivateEnrollmentResponse_Status, error) {
		conn, err := dial(ctx, token)
		if err != nil {
			return denied, err
		}
		return Enroll(ctx, conn, key, token, statusOnly)
	}
	status, err := exchange(true)
	if err != nil {
		return denied, err
	}
	if status == denied && time.Now().Unix() < token.Invitation.ExpiresAt {
		status, err = exchange(false)
		if err != nil {
			return denied, err
		}
	}
	if err := ctx.Err(); err != nil {
		return denied, err
	}
	err = s.update(func(state *State) error {
		for i := range state.Enrollments {
			current := &state.Enrollments[i]
			if current.Link != entry.Link || current.Revision != entry.Revision {
				continue
			}
			if status != irviov1.PrivateEnrollmentResponse_APPROVED {
				current.State = "denied"
				if status == irviov1.PrivateEnrollmentResponse_PENDING {
					current.State = "pending"
				}
				return nil
			}
			target := Target{PeerID: token.Host.String(), Name: current.Name, Locator: hex.EncodeToString(token.Locator[:])}
			found := false
			for j := range state.Targets {
				if state.Targets[j].PeerID == target.PeerID {
					state.Targets[j] = target
					found = true
					break
				}
			}
			if !found {
				state.Targets = append(state.Targets, target)
			}
			state.Enrollments = append(state.Enrollments[:i], state.Enrollments[i+1:]...)
			return nil
		}
		return ErrDenied
	})
	return status, err
}
