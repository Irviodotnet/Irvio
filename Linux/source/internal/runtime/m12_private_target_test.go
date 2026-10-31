// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"crypto/rand"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestBrokeredConnectorTargetIsolation(t *testing.T) {
	identity := func() peer.ID {
		key, _, err := crypto.GenerateEd25519Key(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		id, err := peer.IDFromPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	local, target, other := identity(), identity(), identity()
	base := exitselector.Candidate{PeerID: target, Transport: exitselector.CandidateTransportBrokered, Capabilities: protocol.CapPrivateExit}
	if !validBrokeredTarget(local, target, base) {
		t.Fatal("pinned private candidate rejected")
	}
	if validBrokeredTarget(local, "", base) {
		t.Fatal("private candidate accepted by public connector")
	}
	for _, mutate := range []func(*exitselector.Candidate){
		func(c *exitselector.Candidate) { c.PeerID = other },
		func(c *exitselector.Candidate) { c.PeerID = local },
		func(c *exitselector.Candidate) { c.Country = "NL" },
		func(c *exitselector.Candidate) { c.Capabilities |= protocol.CapExit },
		func(c *exitselector.Candidate) { c.Capabilities = protocol.CapExit },
		func(c *exitselector.Candidate) { c.Transport = exitselector.CandidateTransportDirect },
	} {
		candidate := base
		mutate(&candidate)
		if validBrokeredTarget(local, target, candidate) {
			t.Fatalf("invalid private candidate accepted: %+v", candidate)
		}
	}
	base.Capabilities, base.Country = protocol.CapExit, "NL"
	if !validBrokeredTarget(local, "", base) {
		t.Fatal("public candidate rejected")
	}
}
