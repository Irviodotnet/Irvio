// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestAccessInvitationSingleWinnerPersistsAndRevokes(t *testing.T) {
	store, dir, host := fixture(t)
	now := time.Now()
	invite, err := store.CreateAccessInvitation(now, time.Hour)
	must(t, err)
	var wg sync.WaitGroup
	winners := make(chan peer.ID, 16)
	for i := 0; i < 16; i++ {
		id := identity(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if store.Reserve(invite.ID, invite.Secret, id, now) == nil {
				winners <- id
			}
		}()
	}
	wg.Wait()
	close(winners)
	if len(winners) != 1 {
		t.Fatalf("winners=%d", len(winners))
	}
	winner := <-winners
	must(t, store.Close())
	store, err = Open(dir, host)
	must(t, err)
	defer store.Close()
	must(t, store.PruneInvitations(now.Add(7*24*time.Hour)))
	state, err := store.Snapshot()
	must(t, err)
	if !state.Authorized(winner) {
		t.Fatal("grant expired with invitation or restart")
	}
	must(t, store.Revoke(winner))
	state, err = store.Snapshot()
	must(t, err)
	if state.Authorized(winner) {
		t.Fatal("revoke failed")
	}
	if store.Reserve(invite.ID, invite.Secret, winner, now) == nil {
		t.Fatal("consumed link restored access")
	}
}

func TestAccessInvitationExpiryCancellationAndLegacy(t *testing.T) {
	for _, kind := range []string{"expired", "cancelled", "wrong-secret", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			store, _, _ := fixture(t)
			now, client := time.Now(), identity(t)
			create := store.CreateAccessInvitation
			if kind == "legacy" {
				create = store.CreateInvitation
			}
			invite, err := create(now, time.Hour)
			must(t, err)
			if kind == "expired" {
				now = now.Add(time.Hour)
			}
			if kind == "cancelled" {
				must(t, store.CancelInvitation(invite.ID))
			}
			if kind == "wrong-secret" {
				invite.Secret = "0000000000000000000000000000000000000000000000000000000000000000"
			}
			err = store.Reserve(invite.ID, invite.Secret, client, now)
			if kind == "legacy" {
				must(t, err)
			} else if err == nil {
				t.Fatal("invalid redemption accepted")
			}
			state, err := store.Snapshot()
			must(t, err)
			if state.Authorized(client) {
				t.Fatal("unexpected access")
			}
		})
	}
}
