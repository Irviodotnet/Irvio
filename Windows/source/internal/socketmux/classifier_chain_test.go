// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"net/netip"
	"testing"
)

func TestClassifierChainTerminalSemantics(t *testing.T) {
	source := netip.MustParseAddrPort("127.0.0.1:3478")
	for _, test := range []struct {
		name  string
		chain *ClassifierChain
		want  PacketClass
	}{
		{"empty", NewClassifierChain(), Data},
		{"data-falls-through", NewClassifierChain(
			ClassifierFunc(func([]byte, netip.AddrPort, AddressFamily) PacketClass { return Data }),
			ClassifierFunc(func([]byte, netip.AddrPort, AddressFamily) PacketClass { return Control }),
		), Control},
		{"drop-terminal", NewClassifierChain(
			ClassifierFunc(func([]byte, netip.AddrPort, AddressFamily) PacketClass { return Drop }),
			ClassifierFunc(func([]byte, netip.AddrPort, AddressFamily) PacketClass {
				t.Fatal("classifier after Drop was invoked")
				return Control
			}),
		), Drop},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.chain.Classify([]byte("packet"), source, IPv4); got != test.want {
				t.Fatalf("Classify() = %v, want %v", got, test.want)
			}
		})
	}
}
