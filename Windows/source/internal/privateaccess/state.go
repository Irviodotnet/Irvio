// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	Filename              = "private-access-v1.json"
	LockFilename          = ".private-access-v1.lock"
	MaxBytes              = 1 << 20
	MaxTargets            = 128
	MaxMembers            = 256
	MaxInvitations        = 128
	MaxInvitationLifetime = 24 * time.Hour
)

var (
	ErrInvalid  = errors.New("invalid private access state")
	ErrDenied   = errors.New("private access denied")
	ErrExpired  = errors.New("private invitation expired")
	ErrCapacity = errors.New("private access capacity reached")
	ErrClosed   = errors.New("private access store unavailable")
)

type Target struct {
	PeerID  string `json:"peer_id"`
	Name    string `json:"name"`
	Locator string `json:"locator"`
}

type Member struct {
	PeerID   string `json:"peer_id"`
	Name     string `json:"name"`
	Allowed  bool   `json:"allowed"`
	Revision uint64 `json:"revision"`
}

type Invitation struct {
	AutoApprove bool   `json:"auto_approve,omitempty"`
	ID          string `json:"id"`
	Secret      string `json:"secret,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
	Client      string `json:"client,omitempty"`
	State       string `json:"state"`
}

type State struct {
	Version     int                  `json:"version"`
	HostPeerID  string               `json:"host_peer_id"`
	Locator     string               `json:"locator"`
	Revision    uint64               `json:"revision"`
	Targets     []Target             `json:"targets"`
	Members     []Member             `json:"members"`
	Invitations []Invitation         `json:"invitations"`
	Enrollments []ImportedInvitation `json:"enrollments,omitempty"`
}

type ImportedInvitation struct {
	Name     string `json:"name"`
	Link     string `json:"link"`
	State    string `json:"state"`
	Revision uint64 `json:"revision"`
}

func (ImportedInvitation) String() string { return "[private enrollment redacted]" }

type EnrollmentSummary struct {
	HostPeerID string
	Name       string
	State      string
	ExpiresAt  int64
}

func (s State) EnrollmentSummaries() ([]EnrollmentSummary, error) {
	var result []EnrollmentSummary
	for _, entry := range s.Enrollments {
		token, err := decodeInvitation(entry.Link, time.Now(), true)
		if err != nil {
			return nil, ErrInvalid
		}
		result = append(result, EnrollmentSummary{HostPeerID: token.Host.String(), Name: entry.Name, State: entry.State, ExpiresAt: token.Invitation.ExpiresAt})
	}
	return result, nil
}

func newState(host peer.ID) (State, error) {
	locator, err := randomHex(32)
	return State{Version: 1, HostPeerID: host.String(), Locator: locator, Revision: 1}, err
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func validHex(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size && hex.EncodeToString(decoded) == value
}

func validPeer(value string) bool {
	id, err := peer.Decode(value)
	return err == nil && id.String() == value
}

func validName(value string) bool {
	if len(value) > 80 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (s State) validate() error {
	if (s.Version != 1 && s.Version != 2) || (s.Version == 1 && len(s.Enrollments) != 0) || !validPeer(s.HostPeerID) || !validHex(s.Locator, 32) || s.Revision == 0 || s.Revision == math.MaxUint64 {
		return ErrInvalid
	}
	if len(s.Targets) > MaxTargets || len(s.Members) > MaxMembers || len(s.Invitations) > MaxInvitations || len(s.Enrollments) > MaxTargets {
		return ErrCapacity
	}
	seen := make(map[string]bool)
	for _, enrollment := range s.Enrollments {
		token, err := decodeInvitation(enrollment.Link, time.Now(), true)
		if err != nil || !validName(enrollment.Name) || enrollment.Revision == 0 || enrollment.Revision > s.Revision || token.Host.String() == s.HostPeerID || seen[token.Host.String()] ||
			(enrollment.State != "imported" && enrollment.State != "pending" && enrollment.State != "denied") {
			return ErrInvalid
		}
		seen[token.Host.String()] = true
	}
	clear(seen)
	for _, target := range s.Targets {
		if !validPeer(target.PeerID) || target.PeerID == s.HostPeerID || !validName(target.Name) || !validHex(target.Locator, 32) || seen[target.PeerID] {
			return ErrInvalid
		}
		seen[target.PeerID] = true
	}
	clear(seen)
	for _, member := range s.Members {
		if !validPeer(member.PeerID) || member.PeerID == s.HostPeerID || !validName(member.Name) || member.Revision == 0 || member.Revision > s.Revision || seen[member.PeerID] {
			return ErrInvalid
		}
		seen[member.PeerID] = true
	}
	clear(seen)
	pending := make(map[string]int)
	for _, invite := range s.Invitations {
		if !validHex(invite.ID, 16) || seen[invite.ID] || invite.CreatedAt <= 0 || invite.ExpiresAt <= invite.CreatedAt || invite.ExpiresAt-invite.CreatedAt > int64(MaxInvitationLifetime/time.Second) {
			return ErrInvalid
		}
		seen[invite.ID] = true
		if invite.Client != "" && (!validPeer(invite.Client) || invite.Client == s.HostPeerID) {
			return ErrInvalid
		}
		switch invite.State {
		case "open":
			if invite.Client != "" || !validHex(invite.Secret, 32) {
				return ErrInvalid
			}
		case "pending":
			if invite.Client == "" || !validHex(invite.Secret, 32) {
				return ErrInvalid
			}
			pending[invite.Client]++
			if pending[invite.Client] > 4 {
				return ErrCapacity
			}
		case "consumed":
			if invite.Secret != "" {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}

func (s State) clone() State {
	s.Targets = append([]Target(nil), s.Targets...)
	s.Members = append([]Member(nil), s.Members...)
	s.Invitations = append([]Invitation(nil), s.Invitations...)
	s.Enrollments = append([]ImportedInvitation(nil), s.Enrollments...)
	return s
}

// Authorized is a policy snapshot, not an activation fence or a lease grant.
func (s State) Authorized(id peer.ID) bool {
	for _, member := range s.Members {
		if member.PeerID == id.String() {
			return member.Allowed
		}
	}
	return false
}
