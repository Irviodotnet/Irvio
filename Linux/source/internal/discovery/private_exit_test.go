// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/protocol"
)

func TestPrivateExitReadinessIsolatedFromPublicPublication(t *testing.T) {
	for _, country := range []string{"", "NL"} {
		t.Run(country, func(t *testing.T) {
			node, err := Start(context.Background(), Config{DataDir: discoveryTestDir(t), ListenPort: 0,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Exit: true, Country: country,
				DeferExitAdvertisement: true, AWGPublicKey: bytes.Repeat([]byte{7}, 32), AWGKeyEpoch: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer node.Close()
			if err := node.SetPrivateExitReady(true); err == nil {
				t.Fatal("private Exit activated without session protocols")
			}
			if err := node.EnableSessionEstablishment(); err != nil {
				t.Fatal(err)
			}
			if err := node.SetPrivateExitReady(true); err != nil {
				t.Fatal(err)
			}
			check := func() {
				value, err := protocol.VerifySignedAnnouncement(node.LocalSignedAnnouncement(), node.PeerID(), time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if value.Capabilities&protocol.CapPrivateExit == 0 || value.Capabilities&protocol.CapExit != 0 || value.Country != "" || !value.AcceptingNewSessions || !node.PrivateExitActive() || node.ExitActive() || node.providerLoopStarted {
					t.Fatal("private readiness leaked into public publication")
				}
			}
			check()
			sequence := node.LocalAnnouncement().Sequence
			if err := node.SetPrivateExitReady(true); err != nil {
				t.Fatal(err)
			}
			if node.LocalAnnouncement().Sequence != sequence {
				t.Fatal("idempotent activation changed descriptor")
			}
			if err := node.ActivateExit(context.Background()); err == nil {
				t.Fatal("public Exit activated during private hosting")
			}
			if err := node.SetExitCountry("DE"); err == nil {
				t.Fatal("private descriptor gained country")
			}
			if err := node.UpdateAWGBinding(bytes.Repeat([]byte{8}, 32), 2); err != nil {
				t.Fatal(err)
			}
			if err := node.renewAnnouncement(); err != nil {
				t.Fatal(err)
			}
			check()
			if err := node.SetPrivateExitReady(false); err != nil {
				t.Fatal(err)
			}
			withdrawn := node.LocalAnnouncement()
			if node.PrivateExitActive() || withdrawn.AcceptingNewSessions || withdrawn.Capabilities&(protocol.CapExit|protocol.CapPrivateExit) != 0 || containsFeature(withdrawn.SessionFeatures, protocol.PrivateExitFeature) || withdrawn.Country != country {
				t.Fatal("private withdrawal left capability or lost configured country")
			}
			if country != "" {
				if err := node.ActivateExit(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := node.SetPrivateExitReady(true); err == nil {
					t.Fatal("private activation while public provider active")
				}
				if err := node.SuspendExit(); err != nil {
					t.Fatal(err)
				}
				if err := node.SetPrivateExitReady(true); err != nil {
					t.Fatal(err)
				}
			}
			if err := node.Close(); err != nil {
				t.Fatal(err)
			}
			if node.PrivateExitActive() {
				t.Fatal("closed node still active")
			}
			if err := node.SetPrivateExitReady(true); err == nil {
				t.Fatal("closed node reopened private publication")
			}
		})
	}
}

func TestPrivateExitConcurrentPublicActivation(t *testing.T) {
	node, err := Start(context.Background(), Config{DataDir: discoveryTestDir(t), ListenPort: 0,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Exit: true, Country: "NL",
		DeferExitAdvertisement: true, AWGPublicKey: bytes.Repeat([]byte{7}, 32), AWGKeyEpoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if err := node.EnableSessionEstablishment(); err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	go func() { <-start; results <- node.ActivateExit(context.Background()) }()
	go func() { <-start; results <- node.SetPrivateExitReady(true) }()
	close(start)
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatalf("exactly one activation must succeed: %v / %v", first, second)
	}
	announcement := node.LocalAnnouncement()
	if (announcement.Capabilities&protocol.CapExit != 0) == (announcement.Capabilities&protocol.CapPrivateExit != 0) {
		t.Fatal("concurrent activation produced mixed or missing Exit mode")
	}
	if _, err := protocol.VerifySignedAnnouncement(node.LocalSignedAnnouncement(), node.PeerID(), time.Now()); err != nil {
		t.Fatal(err)
	}
}
