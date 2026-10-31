// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func identity(t *testing.T) peer.ID {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func fixture(t *testing.T) (*Store, string, peer.ID) {
	t.Helper()
	dir, host := privateTestDirectory(t), identity(t)
	s, err := Open(dir, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir, host
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnrollmentPersistenceAndRevocation(t *testing.T) {
	s, dir, host := fixture(t)
	client, now := identity(t), time.Now()
	invite, err := s.CreateInvitation(now, time.Hour)
	must(t, err)
	must(t, s.Reserve(invite.ID, invite.Secret, client, now))
	must(t, s.Reserve(invite.ID, invite.Secret, client, now))
	state, err := s.Snapshot()
	must(t, err)
	if state.Authorized(client) {
		t.Fatal("reservation granted access")
	}
	must(t, s.Close())
	s, err = Open(dir, host)
	must(t, err)
	defer s.Close()
	must(t, s.Approve(invite.ID, "Laptop", now))
	state, err = s.Snapshot()
	must(t, err)
	if !state.Authorized(client) || state.Invitations[0].Secret != "" {
		t.Fatal("approval not committed safely")
	}
	state.Members[0].Allowed = false
	again, err := s.Snapshot()
	must(t, err)
	if !again.Authorized(client) {
		t.Fatal("mutable snapshot escaped")
	}
	must(t, s.Revoke(client))
	if err := s.Reserve(invite.ID, invite.Secret, client, now); !errors.Is(err, ErrDenied) {
		t.Fatalf("replay: %v", err)
	}
	if err := s.Approve(invite.ID, "Laptop", now); !errors.Is(err, ErrDenied) {
		t.Fatalf("reapproval: %v", err)
	}
	state, err = s.Snapshot()
	must(t, err)
	if state.Authorized(client) {
		t.Fatal("revoked member allowed")
	}
}

func TestSingleUseRace(t *testing.T) {
	s, _, _ := fixture(t)
	now := time.Now()
	invite, err := s.CreateInvitation(now, time.Hour)
	must(t, err)
	clients := make([]peer.ID, 32)
	for i := range clients {
		clients[i] = identity(t)
	}
	results := make(chan peer.ID, len(clients))
	var workers sync.WaitGroup
	for _, client := range clients {
		workers.Go(func() {
			if s.Reserve(invite.ID, invite.Secret, client, now) == nil {
				results <- client
			}
		})
	}
	workers.Wait()
	close(results)
	if len(results) != 1 {
		t.Fatalf("reserved by %d clients", len(results))
	}
	winner := <-results
	must(t, s.Revoke(winner))
	if err := s.Approve(invite.ID, "", now); !errors.Is(err, ErrDenied) {
		t.Fatalf("pending revoke bypass: %v", err)
	}
}

func TestExpiryAndCapacity(t *testing.T) {
	s, _, _ := fixture(t)
	now, client := time.Now().Truncate(time.Second), identity(t)
	invite, err := s.CreateInvitation(now, time.Second)
	must(t, err)
	if err := s.Reserve(invite.ID, invite.Secret, client, now.Add(time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry boundary: %v", err)
	}
	for i := 0; i < 5; i++ {
		invite, err = s.CreateInvitation(now, time.Hour)
		must(t, err)
		err = s.Reserve(invite.ID, invite.Secret, client, now)
		if i < 4 {
			must(t, err)
		} else if !errors.Is(err, ErrCapacity) {
			t.Fatalf("pending bound: %v", err)
		}
	}
	must(t, s.PruneInvitations(now.Add(2*time.Hour)))
	for i := 0; i < MaxInvitations; i++ {
		_, err := s.CreateInvitation(now, time.Hour)
		must(t, err)
	}
	if _, err := s.CreateInvitation(now, time.Hour); !errors.Is(err, ErrCapacity) {
		t.Fatalf("invitation bound: %v", err)
	}
}

func TestStrictDecode(t *testing.T) {
	s, _, _ := fixture(t)
	state, err := s.Snapshot()
	must(t, err)
	data, err := json.Marshal(state)
	must(t, err)
	cases := map[string][]byte{
		"unknown":    bytes.Replace(data, []byte(`"version":1`), []byte(`"version":1,"surprise":true`), 1),
		"duplicate":  bytes.Replace(data, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"case-alias": bytes.Replace(data, []byte(`"version":1`), []byte(`"version":1,"Version":1`), 1),
		"version":    bytes.Replace(data, []byte(`"version":1`), []byte(`"version":999`), 1),
		"trailing":   append(append([]byte(nil), data...), []byte(` {}`)...),
		"oversize":   bytes.Repeat([]byte(" "), MaxBytes+1),
		"null":       []byte("null"),
		"deep":       []byte(strings.Repeat("[", 20) + strings.Repeat("]", 20)),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decode(input); err == nil {
				t.Fatal("invalid state accepted")
			}
		})
	}
}

func TestTargetAndMemberCapacityRollback(t *testing.T) {
	s, dir, host := fixture(t)
	must(t, s.update(func(state *State) error {
		for i := 0; i < MaxTargets; i++ {
			state.Targets = append(state.Targets, Target{PeerID: identity(t).String(), Name: strings.Repeat("я", 40), Locator: state.Locator})
		}
		for i := 0; i < MaxMembers; i++ {
			state.Members = append(state.Members, Member{PeerID: identity(t).String(), Allowed: true, Revision: state.Revision})
		}
		return nil
	}))
	now := time.Now().Truncate(time.Second)
	invite, err := s.CreateInvitation(now, time.Hour)
	must(t, err)
	client := identity(t)
	must(t, s.Reserve(invite.ID, invite.Secret, client, now))
	before, err := s.Snapshot()
	must(t, err)
	if err := s.SaveTarget(Target{PeerID: client.String(), Locator: before.Locator}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("target capacity: %v", err)
	}
	if err := s.Approve(invite.ID, "new member", now); !errors.Is(err, ErrCapacity) {
		t.Fatalf("member capacity: %v", err)
	}
	for _, name := range []string{strings.Repeat("я", 40) + "x", "bad\nname", string([]byte{0xff})} {
		target := before.Targets[0]
		target.Name = name
		if err := s.SaveTarget(target); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid display name accepted: %v", err)
		}
	}
	after, err := s.Snapshot()
	must(t, err)
	if !reflect.DeepEqual(before, after) || after.Authorized(client) {
		t.Fatal("rejected mutation changed trust, consumed invitation or advanced revision")
	}
	must(t, s.Close())
	reopened, err := Open(dir, host)
	must(t, err)
	defer reopened.Close()
	after, err = reopened.Snapshot()
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("rejected mutation changed durable state")
	}
}

func TestRevisionExhaustionNeverWrapsOrPersistsMutation(t *testing.T) {
	s, dir, host := fixture(t)
	must(t, s.update(func(state *State) error { state.Revision = math.MaxUint64 - 1; return nil }))
	before, err := s.Snapshot()
	must(t, err)
	if _, err := s.CreateInvitation(time.Now(), time.Hour); !errors.Is(err, ErrInvalid) {
		t.Fatalf("exhausted revision accepted: %v", err)
	}
	after, err := s.Snapshot()
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("revision exhaustion changed in-memory state")
	}
	must(t, s.Close())
	reopened, err := Open(dir, host)
	must(t, err)
	defer reopened.Close()
	after, err = reopened.Snapshot()
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("revision exhaustion changed durable state")
	}
}

func TestFailedWritePoisonsStore(t *testing.T) {
	s, dir, _ := fixture(t)
	must(t, os.Remove(filepath.Join(dir, Filename)))
	must(t, os.Mkdir(filepath.Join(dir, Filename), 0o700))
	if _, err := s.CreateInvitation(time.Now(), time.Hour); err == nil {
		t.Fatal("failed replacement accepted")
	}
	if _, err := s.Snapshot(); !errors.Is(err, ErrClosed) {
		t.Fatal("ambiguous disk state usable")
	}
	if _, err := s.CreateInvitation(time.Now(), time.Hour); !errors.Is(err, ErrClosed) {
		t.Fatal("continued after write failure")
	}
}

func TestCorruptionIsNotReset(t *testing.T) {
	s, dir, host := fixture(t)
	must(t, s.Close())
	path := filepath.Join(dir, Filename)
	broken := []byte(`{"version":999,"secret":"do-not-echo"}`)
	must(t, os.WriteFile(path, broken, 0o600))
	other, err := Open(dir, host)
	if err == nil {
		other.Close()
		t.Fatal("corruption silently reset")
	}
	if strings.Contains(err.Error(), "do-not-echo") {
		t.Fatal("secret in diagnostics")
	}
	after, err := os.ReadFile(path)
	must(t, err)
	if !bytes.Equal(after, broken) {
		t.Fatal("corrupt evidence overwritten")
	}
}

func TestApproveRevokeRace(t *testing.T) {
	s, _, _ := fixture(t)
	now, client := time.Now(), identity(t)
	invite, err := s.CreateInvitation(now, time.Hour)
	must(t, err)
	must(t, s.Reserve(invite.ID, invite.Secret, client, now))
	var workers sync.WaitGroup
	workers.Go(func() {
		err := s.Approve(invite.ID, "", now)
		if err != nil && !errors.Is(err, ErrDenied) {
			t.Error(err)
		}
	})
	workers.Go(func() {
		if err := s.Revoke(client); err != nil {
			t.Error(err)
		}
	})
	workers.Wait()
	state, err := s.Snapshot()
	must(t, err)
	if state.Authorized(client) {
		t.Fatal("approval survived concurrent revoke")
	}
}

func TestApprovalExpiresAndValidationRollsBack(t *testing.T) {
	s, _, _ := fixture(t)
	now, client := time.Now().Truncate(time.Second), identity(t)
	invite, err := s.CreateInvitation(now, time.Minute)
	must(t, err)
	must(t, s.Reserve(invite.ID, invite.Secret, client, now))
	if err := s.Approve(invite.ID, "", now.Add(time.Minute)); !errors.Is(err, ErrExpired) {
		t.Fatalf("late approval: %v", err)
	}
	before, err := s.Snapshot()
	must(t, err)
	if err := s.Approve(invite.ID, "invalid\nname", now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid name: %v", err)
	}
	after, err := s.Snapshot()
	must(t, err)
	if after.Revision != before.Revision || after.Authorized(client) || after.Invitations[0].State != "pending" {
		t.Fatal("failed transaction leaked partial mutation")
	}
}
