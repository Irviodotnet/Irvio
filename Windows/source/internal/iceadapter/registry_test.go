// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"net/netip"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestRetiredRegistryEvictsOldestEntryAtBound(t *testing.T) {
	config := DefaultRegistryConfig()
	config.MaxTransactions = 2
	config.MaxRetired = 1
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.registerUfrag("localA", socketmux.IPv4); err != nil {
		t.Fatal(err)
	}
	server := netip.MustParseAddrPort("192.0.2.1:3478")
	now := time.Now()
	first, second := transactionID{1}, transactionID{2}
	if err := registry.registerTransaction(first, "localA", server, socketmux.IPv4, now); err != nil {
		t.Fatal(err)
	}
	registry.retireTransaction(first, now)
	if err := registry.registerTransaction(second, "localA", server, socketmux.IPv4, now); err != nil {
		t.Fatal(err)
	}
	registry.retireTransaction(second, now)
	if got := registry.classifyResponse(first, server, socketmux.IPv4, now); got != responseUnknown {
		t.Fatalf("evicted retired transaction disposition = %v", got)
	}
	if got := registry.classifyResponse(second, server, socketmux.IPv4, now); got != responseRetired {
		t.Fatalf("newest retired transaction disposition = %v", got)
	}
	_, active, retired := registry.sizes()
	if active != 0 || retired != 1 {
		t.Fatalf("transaction sizes = active:%d retired:%d", active, retired)
	}
}
