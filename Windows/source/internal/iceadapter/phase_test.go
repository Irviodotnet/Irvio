// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

func TestPhaseExclusiveSTUNThenICELeavesDataUsable(t *testing.T) {
	stunRegistry, err := stunprobe.NewRegistry(stunprobe.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	iceRegistry, err := NewRegistry(DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	muxConfig.Classifier = socketmux.NewClassifierChain(stunprobe.NewClassifier(stunRegistry), NewClassifier(iceRegistry))
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()

	server := listenUDP(t, socketmux.IPv4)
	serverDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 2048)
		n, source, readErr := server.ReadFromUDPAddrPort(buffer)
		if readErr != nil {
			serverDone <- readErr
			return
		}
		request := stun.New()
		if decodeErr := request.UnmarshalBinary(buffer[:n]); decodeErr != nil {
			serverDone <- decodeErr
			return
		}
		response := stun.MustBuild(stun.BindingSuccess, &stun.XORMappedAddress{
			IP: source.Addr().AsSlice(), Port: int(source.Port()),
		})
		response.TransactionID = request.TransactionID
		response.Encode()
		_, writeErr := server.WriteToUDPAddrPort(response.Raw, source)
		serverDone <- writeErr
	}()
	stunConfig := stunprobe.DefaultConfig()
	stunConfig.InitialRTO = 25 * time.Millisecond
	stunConfig.MaxRTO = 50 * time.Millisecond
	client, err := stunprobe.NewClient(mux.Control(), stunRegistry, stunConfig)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	result, err := client.Probe(ctx, server.LocalAddr().(*net.UDPAddr).AddrPort())
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if result.Local.Port() != mux.Port() {
		t.Fatalf("STUN phase local port = %d, mux port = %d", result.Local.Port(), mux.Port())
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	adapterConfig := DefaultConfig(netip.MustParseAddr("127.0.0.1"))
	adapterConfig.Logger = muxConfig.Logger
	adapter, err := New(mux.Control(), iceRegistry, adapterConfig)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := adapter.GetConn("localA", adapter.GetListenAddresses()[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}

	sender := listenUDP(t, socketmux.IPv4)
	muxLocal, _ := mux.LocalAddr(socketmux.IPv4)
	if _, err := sender.WriteToUDPAddrPort([]byte("future-data"), muxLocal); err != nil {
		t.Fatal(err)
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	datagram, err := mux.Data().Read(readCtx)
	if err != nil {
		t.Fatal(err)
	}
	if string(datagram.Payload) != "future-data" {
		t.Fatalf("Data payload = %q", datagram.Payload)
	}
}
