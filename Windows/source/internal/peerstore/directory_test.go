// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package peerstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTestDirectory(t *testing.T) *Directory {
	t.Helper()
	directory, err := Open(context.Background(), filepath.Join(t.TempDir(), "hydra.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { directory.Close() })
	return directory
}

func testRecords(nodeID string, sequence uint64, createdAt time.Time) (Peer, Announcement) {
	peer := Peer{
		NodeID:          nodeID,
		ProtocolVersion: 1,
		Capabilities:    3,
		Country:         "DE",
		FirstSeen:       createdAt,
		LastSeen:        createdAt,
		ExpiresAt:       createdAt.Add(time.Hour),
	}
	announcement := Announcement{
		NodeID:         nodeID,
		Incarnation:    1,
		DescriptorID:   []byte(fmt.Sprintf("descriptor-%d", sequence)),
		Epoch:          []byte("0123456789abcdef"),
		Sequence:       sequence,
		SignedEnvelope: []byte(fmt.Sprintf("signed-%d", sequence)),
		CreatedAt:      createdAt,
		ExpiresAt:      createdAt.Add(time.Hour),
	}
	return peer, announcement
}

func TestMigrationAndRoundTrip(t *testing.T) {
	directory := openTestDirectory(t)
	ctx := context.Background()
	version, err := directory.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	now := time.Now().Truncate(time.Second)
	peer, announcement := testRecords("peer-one", 1, now)
	if err := directory.StoreVerified(ctx, peer, announcement); err != nil {
		t.Fatal(err)
	}
	got, err := directory.Peer(ctx, peer.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != peer.NodeID || got.Country != "DE" || got.Capabilities != peer.Capabilities {
		t.Fatalf("unexpected peer: %+v", got)
	}
	envelope, err := directory.SignedAnnouncement(ctx, peer.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if string(envelope) != "signed-1" {
		t.Fatalf("envelope = %q", envelope)
	}
}

func TestStoreVerifiedUpdatesNewerAndRejectsStale(t *testing.T) {
	directory := openTestDirectory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	peer, first := testRecords("peer-one", 1, now)
	if err := directory.StoreVerified(ctx, peer, first); err != nil {
		t.Fatal(err)
	}
	_, repeated := testRecords("peer-one", 1, now.Add(time.Second))
	if err := directory.StoreVerified(ctx, peer, repeated); err != nil {
		t.Fatalf("idempotent repeat error = %v", err)
	}
	repeated.DescriptorID = []byte("conflicting-payload")
	if err := directory.StoreVerified(ctx, peer, repeated); !errors.Is(err, ErrAnnouncementConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	peer.Country = "NL"
	_, newer := testRecords("peer-one", 2, now.Add(2*time.Second))
	if err := directory.StoreVerified(ctx, peer, newer); err != nil {
		t.Fatal(err)
	}
	got, err := directory.Peer(ctx, peer.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Country != "NL" {
		t.Fatalf("country = %q, want NL", got.Country)
	}
}

func TestConcurrentAccess(t *testing.T) {
	directory := openTestDirectory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	var wg sync.WaitGroup
	errorsChannel := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			nodeID := fmt.Sprintf("peer-%02d", index)
			peer, announcement := testRecords(nodeID, 1, now)
			if err := directory.StoreVerified(ctx, peer, announcement); err != nil {
				errorsChannel <- err
				return
			}
			if _, err := directory.Peer(ctx, nodeID); err != nil {
				errorsChannel <- err
			}
		}(i)
	}
	wg.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Error(err)
	}
}

func TestAWGBindingPersistsAndRejectsRollback(t *testing.T) {
	directory := openTestDirectory(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	record, announcement := testRecords("peer-awg", 1, now)
	record.AWGPublicKey = bytes.Repeat([]byte{7}, 32)
	record.AWGKeyEpoch = 7
	if err := directory.StoreVerified(ctx, record, announcement); err != nil {
		t.Fatal(err)
	}
	record.AWGPublicKey = bytes.Repeat([]byte{8}, 32)
	record.AWGKeyEpoch = 8
	_, announcement = testRecords("peer-awg", 2, now.Add(time.Second))
	if err := directory.StoreVerified(ctx, record, announcement); err != nil {
		t.Fatal(err)
	}
	stored, err := directory.Peer(ctx, record.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AWGKeyEpoch != 8 || !bytes.Equal(stored.AWGPublicKey, record.AWGPublicKey) {
		t.Fatalf("stored AWG binding = epoch %d key %x", stored.AWGKeyEpoch, stored.AWGPublicKey)
	}
	record.AWGPublicKey = bytes.Repeat([]byte{9}, 32)
	_, announcement = testRecords("peer-awg", 3, now.Add(2*time.Second))
	if err := directory.StoreVerified(ctx, record, announcement); !errors.Is(err, ErrStaleAWGBinding) {
		t.Fatalf("same-epoch conflicting key error = %v", err)
	}
	record.AWGKeyEpoch = 7
	if err := directory.StoreVerified(ctx, record, announcement); !errors.Is(err, ErrStaleAWGBinding) {
		t.Fatalf("rollback error = %v", err)
	}
}
