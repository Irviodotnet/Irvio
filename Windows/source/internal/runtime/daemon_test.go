// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestDaemonOwnsPersistentIdentitiesDiscoveryAndNetworkMonitor(t *testing.T) {
	dataDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := func(rotate bool) *Daemon {
		daemon, err := NewDaemon(DaemonConfig{
			DataDir: dataDir, ListenPort: 0,
			DHTMode: "server", Logger: logger, RotateAWGKey: rotate,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		if err := daemon.Start(ctx); err != nil {
			t.Fatal(err)
		}
		return daemon
	}
	first := start(false)
	firstPeer := first.Node().PeerID()
	firstAnnouncement := first.Node().LocalAnnouncement()
	if len(firstAnnouncement.AwgPublicKey) != 32 || firstAnnouncement.AwgKeyEpoch != 1 ||
		first.Status().NetworkGeneration == 0 {
		t.Fatalf("incomplete production startup: announcement=%+v status=%+v", firstAnnouncement, first.Status())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := first.Close(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()

	second := start(true)
	secondAnnouncement := second.Node().LocalAnnouncement()
	if second.Node().PeerID() != firstPeer {
		t.Fatal("persistent Irvio identity changed across daemon restart")
	}
	if secondAnnouncement.AwgKeyEpoch != 2 ||
		string(secondAnnouncement.AwgPublicKey) == string(firstAnnouncement.AwgPublicKey) {
		t.Fatal("explicit AWG rotation did not advance the independent key epoch")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := second.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
