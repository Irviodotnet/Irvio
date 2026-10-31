// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

func TestPrivateEnrollmentEndpointOwnerFencingAndShutdown(t *testing.T) {
	exit, _ := newRendezvousHost(t)
	owner, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, owner, exit)
	started := make(chan struct{}, 8)
	ended := make(chan struct{}, 8)
	var active atomic.Int32
	endpoint, err := NewPrivateEnrollmentEndpoint(exit, func(ctx context.Context, conn net.Conn) error {
		active.Add(1)
		defer active.Add(-1)
		defer func() { ended <- struct{}{} }()
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	privateMust(t, err)
	defer endpoint.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	open := func() (network.Stream, error) {
		stream, err := owner.NewStream(ctx, exit.ID(), libprotocol.ID(PrivateEnrollmentEndpointProtocol))
		if err != nil {
			return nil, err
		}
		stream.SetDeadline(time.Now().Add(time.Second))
		_, _ = stream.Write([]byte{1})
		return stream, nil
	}
	denied, deniedErr := open()
	if deniedErr == nil {
		var value [1]byte
		if _, err := denied.Read(value[:]); err == nil {
			t.Fatal("unknown owner admitted")
		}
		denied.Reset()
	}
	select {
	case <-started:
		t.Fatal("unknown owner reached enrollment")
	default:
	}
	privateMust(t, endpoint.SetOwners([]peer.ID{owner.ID()}))
	first, err := open()
	privateMust(t, err)
	defer first.Reset()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("allowed owner not admitted")
	}
	privateMust(t, endpoint.SetOwners(nil))
	select {
	case <-ended:
	case <-ctx.Done():
		t.Fatal("removed owner not canceled")
	}
	privateMust(t, endpoint.SetOwners([]peer.ID{owner.ID()}))
	second, err := open()
	privateMust(t, err)
	defer second.Reset()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("replacement owner not admitted")
	}
	done := make(chan struct{})
	go func() { endpoint.Close(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("shutdown did not join enrollment")
	}
	if active.Load() != 0 {
		t.Fatal("enrollment worker survived shutdown")
	}
	if err := endpoint.SetOwners([]peer.ID{owner.ID()}); err != ErrClosed {
		t.Fatal("closed endpoint reopened")
	}
}
