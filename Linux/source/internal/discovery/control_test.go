// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
)

func discoveryTestDir(t *testing.T) string { t.Helper(); return filepath.Join(t.TempDir(), "profile") }

func TestDiscoveryRejectsAmbiguousControlPolicyBeforeCreatingState(t *testing.T) {
	path := discoveryTestDir(t)
	factory := func(multiaddr.Multiaddr) (tcptransport.ContextDialer, error) {
		t.Fatal("ambiguous dialer invoked")
		return nil, nil
	}
	if _, err := Start(context.Background(), Config{DataDir: path, ControlMark: 1, ControlDialer: factory}); err == nil {
		t.Fatal("ambiguous policy accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid configuration created state: %v", err)
	}
}
