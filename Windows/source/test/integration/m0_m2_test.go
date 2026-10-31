// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package integration_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/discovery"

	_ "modernc.org/sqlite"
)

func TestM0M2ThreeNodeDiscoverySurvivesBootstrapRemoval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	b, err := discovery.Start(ctx, discovery.Config{
		DataDir: filepath.Join(t.TempDir(), "profile"), ListenPort: 0, Logger: logger,
	})
	if err != nil {
		t.Fatalf("start B: %v", err)
	}
	bClosed := false
	defer func() {
		if !bClosed {
			_ = b.Close()
		}
	}()
	bootstrapAddress, err := b.IPv4LoopbackAddress()
	if err != nil {
		t.Fatal(err)
	}

	c, err := discovery.Start(ctx, discovery.Config{
		DataDir: filepath.Join(t.TempDir(), "profile"), ListenPort: 0, Bootstrap: []string{bootstrapAddress},
		Exit: true, Country: "DE", Logger: logger, ProviderRefresh: time.Second,
	})
	if err != nil {
		t.Fatalf("start C: %v", err)
	}
	defer c.Close()

	a, err := discovery.Start(ctx, discovery.Config{
		DataDir: filepath.Join(t.TempDir(), "profile"), ListenPort: 0, Bootstrap: []string{bootstrapAddress}, Logger: logger,
	})
	if err != nil {
		t.Fatalf("start A: %v", err)
	}
	defer a.Close()
	if a.PeerID() == b.PeerID() || a.PeerID() == c.PeerID() || b.PeerID() == c.PeerID() {
		t.Fatal("independent nodes did not receive distinct identities")
	}

	db, err := sql.Open("sqlite", a.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var peerCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM peers").Scan(&peerCount); err != nil {
		t.Fatal(err)
	}
	if peerCount != 0 {
		t.Fatalf("A initially has %d persisted peers, want 0", peerCount)
	}

	discoveryCtx, discoveryCancel := context.WithTimeout(ctx, 20*time.Second)
	providers, err := a.FindExits(discoveryCtx, "DE")
	discoveryCancel()
	if err != nil {
		t.Fatalf("A find EXIT/DE: %v", err)
	}
	if len(providers) != 1 || providers[0] != c.PeerID() {
		t.Fatalf("A providers = %v, want only C %s", providers, c.PeerID())
	}
	var storedNodeID string
	if err := db.QueryRowContext(ctx, "SELECT node_id FROM peers WHERE node_id = ?", c.PeerID().String()).Scan(&storedNodeID); err != nil {
		t.Fatalf("query A SQLite for C: %v", err)
	}
	if storedNodeID != c.PeerID().String() {
		t.Fatalf("stored NodeID = %q, want %s", storedNodeID, c.PeerID())
	}

	if err := b.Close(); err != nil {
		t.Fatalf("kill bootstrap B: %v", err)
	}
	bClosed = true
	directCtx, directCancel := context.WithTimeout(ctx, 10*time.Second)
	defer directCancel()
	if err := a.VerifyDirect(directCtx, c.PeerID()); err != nil {
		t.Fatalf("A cannot communicate directly with C after B removal: %v", err)
	}
}
