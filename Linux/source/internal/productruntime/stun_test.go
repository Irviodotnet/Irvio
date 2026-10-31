// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestBootstrapSTUNIPv4UsesEveryDirectPublicPeer(t *testing.T) {
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	suffix := "/p2p/" + id.String()
	addresses := []string{
		"/ip4/89.124.71.34/tcp/4000" + suffix,
		"/ip4/89.124.71.34/tcp/4001" + suffix,
		"/ip4/1.1.1.1/tcp/4000" + suffix,
		"/ip4/10.0.0.1/tcp/4000" + suffix,
		"/ip4/198.51.100.1/tcp/4000" + suffix,
		"/ip6/2606:4700:4700::1111/tcp/4000" + suffix,
		"/ip4/8.8.8.8/tcp/4000/p2p/" + id.String() + "/p2p-circuit",
	}
	got := BootstrapSTUNIPv4(addresses)
	want := []netip.AddrPort{netip.MustParseAddrPort("89.124.71.34:3478"), netip.MustParseAddrPort("1.1.1.1:3478")}
	if !slices.Equal(got, want) {
		t.Fatalf("STUN pool = %v, want %v", got, want)
	}
}
