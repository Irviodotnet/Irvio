// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	awgconn "github.com/amnezia-vpn/amneziawg-go/v3/conn"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestIrvioBindUsesMuxDataAndPhysicalSourcePort(t *testing.T) {
	mux := newDataMux(t, netip.MustParseAddr("127.0.0.1"), false)
	bind, err := NewIrvioBind(mux, networkstate.Generation(1))
	if err != nil {
		t.Fatal(err)
	}
	receivers, port, err := bind.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	if port != mux.Port() || len(receivers) != 1 {
		t.Fatalf("open result port=%d receivers=%d mux=%d", port, len(receivers), mux.Port())
	}
	if _, _, err := bind.Open(port); !errors.Is(err, awgconn.ErrBindAlreadyOpen) {
		t.Fatalf("second open error = %v", err)
	}

	sender, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	local, _ := mux.LocalAddr(socketmux.IPv4)
	input := []byte("real-data-path")
	if _, err := sender.WriteToUDPAddrPort(input, local); err != nil {
		t.Fatal(err)
	}
	packets := [][]byte{make([]byte, 2048)}
	sizes := make([]int, 1)
	endpoints := make([]awgconn.Endpoint, 1)
	received := make(chan error, 1)
	go func() {
		count, readErr := receivers[0](packets, sizes, endpoints)
		if readErr == nil && count != 1 {
			readErr = errors.New("unexpected receive count")
		}
		received <- readErr
	}()
	select {
	case err := <-received:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("IrvioBind receive timed out")
	}
	if got := string(packets[0][:sizes[0]]); got != string(input) {
		t.Fatalf("payload = %q", got)
	}
	if endpoints[0].DstToString() != sender.LocalAddr().String() {
		t.Fatalf("source endpoint = %s", endpoints[0].DstToString())
	}

	receiver, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	destination := receiver.LocalAddr().(*net.UDPAddr).AddrPort()
	parsed, err := bind.ParseEndpoint(destination.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := bind.Send([][]byte{[]byte("outbound")}, parsed); err != nil {
		t.Fatal(err)
	}
	if err := receiver.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	_, source, err := receiver.ReadFromUDPAddrPort(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if source.Port() != mux.Port() {
		t.Fatalf("physical source port = %d, mux port = %d", source.Port(), mux.Port())
	}
	if err := bind.SetMark(0); err != nil {
		t.Fatalf("SetMark(0): %v", err)
	}
	if err := bind.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bind.Send([][]byte{[]byte("closed")}, parsed); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("send after close error = %v", err)
	}
	if err := bind.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := bind.Open(port); !errors.Is(err, ErrClosed) {
		t.Fatalf("open after shutdown error = %v", err)
	}
}

func TestIrvioBindCloseUnblocksReceiveAndLeavesMuxDataUsable(t *testing.T) {
	mux := newDataMux(t, netip.MustParseAddr("127.0.0.1"), false)
	bind, err := NewIrvioBind(mux, 1)
	if err != nil {
		t.Fatal(err)
	}
	receivers, _, err := bind.Open(mux.Port())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, readErr := receivers[0]([][]byte{make([]byte, 2048)}, make([]int, 1), make([]awgconn.Endpoint, 1))
		done <- readErr
	}()
	if err := bind.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("receive close error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("receive did not unblock")
	}

	sender, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	local, _ := mux.LocalAddr(socketmux.IPv4)
	if _, err := sender.WriteToUDPAddrPort([]byte("after-bind"), local); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	datagram, err := mux.Data().Read(ctx)
	if err != nil || string(datagram.Payload) != "after-bind" {
		t.Fatalf("mux data after bind close: payload=%q err=%v", datagram.Payload, err)
	}
}

func newDataMux(t *testing.T, address netip.Addr, dual bool) *socketmux.Mux {
	return newDataMuxWithClassifier(t, address, dual, socketmux.ClassifierFunc(func([]byte, netip.AddrPort, socketmux.AddressFamily) socketmux.PacketClass {
		return socketmux.Data
	}))
}

func newDataMuxWithClassifier(t *testing.T, address netip.Addr, dual bool, classifier socketmux.Classifier) *socketmux.Mux {
	t.Helper()
	config := socketmux.DefaultConfig()
	config.Port = 0
	config.IPv4Policy = socketmux.FamilyDisabled
	config.IPv6Policy = socketmux.FamilyDisabled
	if address.Is4() {
		config.IPv4Policy = socketmux.FamilyRequired
		config.IPv4Bind = address
	} else {
		config.IPv6Policy = socketmux.FamilyRequired
		config.IPv6Bind = address
	}
	if dual {
		config.IPv4Policy = socketmux.FamilyRequired
		config.IPv4Bind = netip.MustParseAddr("127.0.0.1")
		config.IPv6Policy = socketmux.FamilyRequired
		config.IPv6Bind = netip.MustParseAddr("::1")
	}
	config.Classifier = classifier
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mux.Close() })
	return mux
}
