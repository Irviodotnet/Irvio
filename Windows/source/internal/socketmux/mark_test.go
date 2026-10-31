// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"context"
	"errors"
	"net"
	"testing"
)

type markCall struct {
	conn *net.UDPConn
	mark uint32
}

func TestSocketMuxSetMarkRollsBackEarlierFamily(t *testing.T) {
	mux, err := New(context.Background(), dualStackConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()

	injected := errors.New("injected IPv6 SO_MARK failure")
	calls := make([]markCall, 0, 3)
	mux.setMark = func(conn *net.UDPConn, mark uint32) error {
		calls = append(calls, markCall{conn: conn, mark: mark})
		if conn == mux.ipv6.conn && mark == 0x42 {
			return injected
		}
		return nil
	}
	if err := mux.SetMark(0x42); !errors.Is(err, injected) {
		t.Fatalf("SetMark error = %v", err)
	}
	if len(calls) != 3 || calls[0] != (markCall{conn: mux.ipv4.conn, mark: 0x42}) ||
		calls[1] != (markCall{conn: mux.ipv6.conn, mark: 0x42}) ||
		calls[2] != (markCall{conn: mux.ipv4.conn, mark: 0}) {
		t.Fatalf("SO_MARK calls = %#v", calls)
	}
	if mux.Mark() != 0 {
		t.Fatalf("failed transaction published mark %#x", mux.Mark())
	}
}

func TestSocketMuxSetMarkZeroIsIdempotent(t *testing.T) {
	config := ipv4Config()
	mux, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	if err := mux.SetMark(0); err != nil {
		t.Fatalf("SetMark(0): %v", err)
	}
	if got := mux.Mark(); got != 0 {
		t.Fatalf("Mark() = %#x, want 0", got)
	}
	if err := mux.SetMark(0); err != nil {
		t.Fatalf("repeated SetMark(0): %v", err)
	}
}

func TestSocketMuxSetMarkAfterCloseFails(t *testing.T) {
	config := ipv4Config()
	mux, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := mux.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mux.SetMark(1); err == nil {
		t.Fatal("SetMark after Close succeeded")
	}
}
