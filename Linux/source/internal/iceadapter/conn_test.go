// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestLogicalPacketConnOutboundUsesMuxPort(t *testing.T) {
	stack := newTestStack(t, true, 8)
	for _, test := range []struct {
		name    string
		family  socketmux.AddressFamily
		address netip.Addr
	}{
		{"IPv4", socketmux.IPv4, netip.MustParseAddr("127.0.0.1")},
		{"IPv6", socketmux.IPv6, netip.MustParseAddr("::1")},
	} {
		t.Run(test.name, func(t *testing.T) {
			receiver := listenUDP(t, test.family)
			connection, err := stack.adapter.GetConn("localA", net.UDPAddrFromAddrPort(netip.AddrPortFrom(test.address, stack.mux.Port())))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			request := mustBindingRequest(t, "remoteA:localA")
			destination := receiver.LocalAddr().(*net.UDPAddr)
			if _, err := connection.WriteTo(request.Raw, destination); err != nil {
				t.Fatal(err)
			}
			payload, source := readUDP(t, receiver)
			if source.Port() != stack.mux.Port() {
				t.Fatalf("source port = %d, mux port = %d", source.Port(), stack.mux.Port())
			}
			if string(payload) != string(request.Raw) {
				t.Fatal("physical receiver got different payload")
			}
			snapshot, exists := stack.registry.Transaction(request.TransactionID)
			if !exists || snapshot.LocalUfrag != "localA" || snapshot.Remote != destination.AddrPort() || snapshot.Family != test.family {
				t.Fatalf("transaction snapshot = %+v, exists=%v", snapshot, exists)
			}
		})
	}
}

func TestLogicalPacketConnRejectsApplicationData(t *testing.T) {
	stack := newTestStack(t, false, 8)
	receiver := listenUDP(t, socketmux.IPv4)
	connection, err := stack.adapter.GetConn("localA", stack.adapter.GetListenAddresses()[0])
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.WriteTo([]byte("application-data"), receiver.LocalAddr()); !errors.Is(err, ErrNonICEPayloadUnsupported) {
		t.Fatalf("WriteTo error = %v", err)
	}
	if err := receiver.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := receiver.ReadFromUDP(make([]byte, 64)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("unexpected physical read result: %v", err)
	}
}

func TestDispatcherRoutesRequestsAndResponsesByUfrag(t *testing.T) {
	stack := newTestStack(t, false, 8)
	local := stack.adapter.GetListenAddresses()[0]
	conn1, err := stack.adapter.GetConn("localA", local)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()
	conn2, err := stack.adapter.GetConn("localB", local)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	sender := listenUDP(t, socketmux.IPv4)
	muxLocal, _ := stack.mux.LocalAddr(socketmux.IPv4)
	request1 := mustBindingRequest(t, "localA:remoteA")
	request2 := mustBindingRequest(t, "localB:remoteB")
	if _, err := sender.WriteToUDPAddrPort(request1.Raw, muxLocal); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.WriteToUDPAddrPort(request2.Raw, muxLocal); err != nil {
		t.Fatal(err)
	}
	assertReadTransaction(t, conn1, request1.TransactionID)
	assertReadTransaction(t, conn2, request2.TransactionID)

	server1 := listenUDP(t, socketmux.IPv4)
	server2 := listenUDP(t, socketmux.IPv4)
	out1 := mustBindingRequest(t, "remoteA:localA")
	out2 := mustBindingRequest(t, "remoteB:localB")
	if _, err := conn1.WriteTo(out1.Raw, server1.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn2.WriteTo(out2.Raw, server2.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	readUDP(t, server1)
	readUDP(t, server2)
	response2 := mustBindingResponse(t, out2)
	response1 := mustBindingResponse(t, out1)
	if _, err := server2.WriteToUDPAddrPort(response2.Raw, muxLocal); err != nil {
		t.Fatal(err)
	}
	if _, err := server1.WriteToUDPAddrPort(response1.Raw, muxLocal); err != nil {
		t.Fatal(err)
	}
	assertReadTransaction(t, conn2, out2.TransactionID)
	assertReadTransaction(t, conn1, out1.TransactionID)
}

func TestRepeatedGetConnAndRemoveByUfrag(t *testing.T) {
	stack := newTestStack(t, false, 8)
	local := stack.adapter.GetListenAddresses()[0]
	first, err := stack.adapter.GetConn("localA", local)
	if err != nil {
		t.Fatal(err)
	}
	second, err := stack.adapter.GetConn("localA", local)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	sender := listenUDP(t, socketmux.IPv4)
	muxLocal, _ := stack.mux.LocalAddr(socketmux.IPv4)
	request := mustBindingRequest(t, "localA:remoteA")
	if _, err := sender.WriteToUDPAddrPort(request.Raw, muxLocal); err != nil {
		t.Fatal(err)
	}
	assertReadTransaction(t, second, request.TransactionID)
	stack.adapter.RemoveConnByUfrag("localA")
	if _, _, err := second.ReadFrom(make([]byte, 1500)); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadFrom after RemoveConnByUfrag = %v", err)
	}
}

func TestPacketConnDeadlinesAndClose(t *testing.T) {
	stack := newTestStack(t, false, 8)
	connection, err := stack.adapter.GetConn("localA", stack.adapter.GetListenAddresses()[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.ReadFrom(make([]byte, 1500)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expired read deadline error = %v", err)
	}
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := connection.SetWriteDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	receiver := listenUDP(t, socketmux.IPv4)
	request := mustBindingRequest(t, "remoteA:localA")
	if _, err := connection.WriteTo(request.Raw, receiver.LocalAddr()); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expired write deadline error = %v", err)
	}
	if err := connection.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	request = mustBindingRequest(t, "remoteA:localA")
	if _, err := connection.WriteTo(request.Raw, receiver.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	readUDP(t, receiver)
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLogicalQueueBackpressureIsolation(t *testing.T) {
	stack := newTestStack(t, false, 1)
	local := stack.adapter.GetListenAddresses()[0]
	conn1, err := stack.adapter.GetConn("localA", local)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()
	conn2, err := stack.adapter.GetConn("localB", local)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	sender := listenUDP(t, socketmux.IPv4)
	muxLocal, _ := stack.mux.LocalAddr(socketmux.IPv4)
	for index := 0; index < 3; index++ {
		request := mustBindingRequest(t, "localA:remoteA")
		if _, err := sender.WriteToUDPAddrPort(request.Raw, muxLocal); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, time.Second, func() bool { return stack.adapter.Stats().LogicalQueueDrops > 0 })
	request2 := mustBindingRequest(t, "localB:remoteB")
	if _, err := sender.WriteToUDPAddrPort(request2.Raw, muxLocal); err != nil {
		t.Fatal(err)
	}
	assertReadTransaction(t, conn2, request2.TransactionID)
}

func assertReadTransaction(t *testing.T, connection net.PacketConn, id [stun.TransactionIDSize]byte) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 2048)
	n, _, err := connection.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	message := stun.New()
	if err := message.UnmarshalBinary(buffer[:n]); err != nil {
		t.Fatal(err)
	}
	if message.TransactionID != id {
		t.Fatalf("transaction ID = %x, want %x", message.TransactionID, id)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true before deadline")
		}
		time.Sleep(time.Millisecond)
	}
}
