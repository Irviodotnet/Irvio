// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestAdapterCloseUnblocksReadersAndLeavesDataUsable(t *testing.T) {
	stack := newTestStack(t, false, 8)
	connection, err := stack.adapter.GetConn("localA", stack.adapter.GetListenAddresses()[0])
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, _, readErr := connection.ReadFrom(make([]byte, 1500))
		readDone <- readErr
	}()
	if err := stack.adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stack.adapter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("blocked read error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("logical reader remained blocked")
	}

	sender := listenUDP(t, socketmux.IPv4)
	muxLocal, _ := stack.mux.LocalAddr(socketmux.IPv4)
	if _, err := sender.WriteToUDPAddrPort([]byte("data-after-ice"), muxLocal); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	datagram, err := stack.mux.Data().Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(datagram.Payload) != "data-after-ice" {
		t.Fatalf("Data payload = %q", datagram.Payload)
	}
}

func TestMuxCloseFirstTerminatesAdapter(t *testing.T) {
	stack := newTestStack(t, false, 8)
	connection, err := stack.adapter.GetConn("localA", stack.adapter.GetListenAddresses()[0])
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, _, readErr := connection.ReadFrom(make([]byte, 1500))
		readDone <- readErr
	}()
	if err := stack.mux.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if !errors.Is(err, ErrTransportClosed) {
			t.Fatalf("blocked read error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("logical reader remained blocked after mux close")
	}
	if err := stack.adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := stack.adapter.GetConn("localB", &net.UDPAddr{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("GetConn after close error = %v", err)
	}
}
