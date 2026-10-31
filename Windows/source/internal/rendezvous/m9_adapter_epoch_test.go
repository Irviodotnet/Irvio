// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/protocol"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func TestDescriptorEpochIgnoresDescriptorRefresh(t *testing.T) {
	announcement := &irviov1.NodeAnnouncement{
		SessionEpoch: bytes.Repeat([]byte{0x31}, protocol.SessionEpochSize),
		Libp2PAddrs:  []string{"/ip4/198.51.100.10/tcp/4001"},
	}
	first := Descriptor{Announcement: announcement, Version: DescriptorVersion{Incarnation: 7, Sequence: 10}}
	first.ID[0] = 1
	refreshed := Descriptor{Announcement: announcement, Version: DescriptorVersion{Incarnation: 8, Sequence: 99}}
	refreshed.ID[0] = 2
	if got, want := descriptorEpoch(refreshed), descriptorEpoch(first); got != want {
		t.Fatalf("descriptor refresh changed connectivity epoch: %d != %d", got, want)
	}
}

func TestDescriptorEpochChangesWithAdvertisedEndpoint(t *testing.T) {
	session := bytes.Repeat([]byte{0x31}, protocol.SessionEpochSize)
	first := Descriptor{Announcement: &irviov1.NodeAnnouncement{
		SessionEpoch: session,
		Libp2PAddrs:  []string{"/ip4/198.51.100.10/tcp/4001"},
	}}
	changed := Descriptor{Announcement: &irviov1.NodeAnnouncement{
		SessionEpoch: session,
		Libp2PAddrs:  []string{"/ip4/198.51.100.11/tcp/4001"},
	}}
	if descriptorEpoch(first) == descriptorEpoch(changed) {
		t.Fatal("remote endpoint change did not change connectivity epoch")
	}
}
