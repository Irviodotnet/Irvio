// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

func connectedHosts(t *testing.T) (host.Host, host.Host) {
	t.Helper()
	first, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = first.Close()
		_ = second.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := first.Connect(ctx, peer.AddrInfo{ID: second.ID(), Addrs: second.Addrs()}); err != nil {
		t.Fatal(err)
	}
	return first, second
}

func TestAnnouncementStreamRoundTrip(t *testing.T) {
	client, serverHost := connectedHosts(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := RegisterAnnouncementServer(serverHost, logger)
	want := []byte("bounded signed envelope")
	if err := server.SetEnvelope(want); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := FetchSignedAnnouncement(ctx, client, serverHost.ID())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("response = %q, want %q", got, want)
	}
}

func TestAnnouncementStreamRejectsOversizedLengthBeforeAllocation(t *testing.T) {
	client, maliciousHost := connectedHosts(t)
	maliciousHost.SetStreamHandler(NodeProtocolID, func(stream network.Stream) {
		defer stream.Close()
		_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
		request := []byte{0}
		if _, err := io.ReadFull(stream, request); err != nil {
			return
		}
		_, _ = stream.Write(binary.AppendUvarint(nil, MaxSignedEnvelopeSize+1))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := FetchSignedAnnouncement(ctx, client, maliciousHost.ID()); err == nil {
		t.Fatal("oversized response length was accepted")
	}
}
