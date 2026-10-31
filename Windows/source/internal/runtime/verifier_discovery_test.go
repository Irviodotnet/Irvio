// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"crypto/rand"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestDirectIPv4BootstrapVerifierSelection(t *testing.T) {
	_, key, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		address   string
		allowTest bool
		want      bool
	}{
		{"/ip4/89.124.71.34/tcp/4000", false, true},
		{"/ip4/192.168.23.2/tcp/4000", false, false},
		{"/ip4/198.18.1.2/tcp/4000", false, false},
		{"/ip4/198.18.1.2/tcp/4000", true, true},
		{"/ip6/2001:db8::1/tcp/4000", false, false},
		{"/ip4/89.124.71.34/udp/4000", false, false},
	} {
		address, err := ma.NewMultiaddr(item.address)
		if err != nil {
			t.Fatal(err)
		}
		value, _, ok := directIPv4Verifier(id, address, item.allowTest)
		if ok != item.want {
			t.Fatalf("%s allowTest=%t selected=%t", item.address, item.allowTest, ok)
		}
		if ok && value != item.address+"/p2p/"+id.String() {
			t.Fatalf("verifier address=%q", value)
		}
	}
}
