// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package underlay

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/linuxroute"
)

func TestMarkedChallengeRejectsWrongMarkAndMismatchedResponse(t *testing.T) {
	if _, err := NewMarkedChallengeProbe(linuxroute.IrvioBypassMark + 1); err == nil {
		t.Fatal("accepted non-Irvio bypass mark")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		request := make([]byte, challengeSize)
		_, _ = connection.Read(request)
		request[len(request)-1] ^= 1
		_, _ = connection.Write(request)
	}()
	probe, err := NewMarkedChallengeProbe(linuxroute.IrvioBypassMark)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := netip.MustParseAddrPort(listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := probe.Probe(ctx, Target{Endpoint: endpoint}); err == nil {
		t.Fatal("mismatched nonce was accepted")
	}
}
