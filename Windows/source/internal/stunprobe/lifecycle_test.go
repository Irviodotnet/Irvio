// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestInFlightLimitRejectsWithoutAffectingActiveProbe(t *testing.T) {
	registryConfig := RegistryConfig{MaxInFlight: 1, MaxRetired: 8, RetiredGrace: time.Second}
	_, _, client := newTestStack(t, false, registryConfig, Config{InitialRTO: time.Second, MaxRTO: time.Second, MaxAttempts: 1})
	gate := make(chan struct{})
	activeServer := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{gate: gate})
	otherServer := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	ctx, cancel := context.WithCancel(context.Background())
	activeDone := make(chan error, 1)
	go func() {
		_, err := client.Probe(ctx, activeServer.addr)
		activeDone <- err
	}()
	nextRequest(t, activeServer)
	if _, err := client.Probe(context.Background(), otherServer.addr); !errors.Is(err, ErrInFlightLimit) {
		t.Fatalf("in-flight rejection = %v", err)
	}
	cancel()
	if err := <-activeDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("active probe error = %v", err)
	}
	if stats := client.Stats(); stats.ProbesRejectedInFlight != 1 {
		t.Fatalf("in-flight rejections = %d", stats.ProbesRejectedInFlight)
	}
}

func TestClientCloseUnblocksProbesAndLeavesMuxUsable(t *testing.T) {
	_, mux, client := newTestStack(t, false, testRegistryConfig(), Config{InitialRTO: time.Second, MaxRTO: time.Second, MaxAttempts: 1})
	gate := make(chan struct{})
	server := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{gate: gate})
	probeDone := make(chan error, 1)
	go func() {
		_, err := client.Probe(context.Background(), server.addr)
		probeDone <- err
	}()
	nextRequest(t, server)

	var closeWG sync.WaitGroup
	closeErrors := make(chan error, 8)
	for range 8 {
		closeWG.Add(1)
		go func() {
			defer closeWG.Done()
			closeErrors <- client.Close()
		}()
	}
	closeWG.Wait()
	close(closeErrors)
	for err := range closeErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := <-probeDone; !errors.Is(err, ErrClosed) {
		t.Fatalf("probe after client close error = %v", err)
	}
	if _, err := client.Probe(context.Background(), server.addr); !errors.Is(err, ErrClosed) {
		t.Fatalf("new probe after close error = %v", err)
	}

	sender, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	destination, _ := mux.LocalAddr(socketmux.IPv4)
	if _, err := sender.WriteToUDPAddrPort([]byte("data-after-client-close"), destination); err != nil {
		t.Fatal(err)
	}
	if datagram := readData(t, mux); string(datagram.Payload) != "data-after-client-close" {
		t.Fatalf("Data payload after client close = %q", datagram.Payload)
	}

	receiver, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	receiverAddr := receiver.LocalAddr().(*net.UDPAddr).AddrPort()
	if _, err := mux.Control().WriteTo(context.Background(), []byte("control-write-after-close"), receiverAddr); err != nil {
		t.Fatal(err)
	}
	if err := receiver.SetReadDeadline(time.Now().Add(testDeadline)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	n, source, err := receiver.ReadFromUDPAddrPort(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "control-write-after-close" || source.Port() != mux.Port() {
		t.Fatalf("post-close Control write payload=%q source=%s", buffer[:n], source)
	}
}

func TestMuxCloseStopsDispatcherAndActiveProbe(t *testing.T) {
	_, mux, client := newTestStack(t, false, testRegistryConfig(), Config{InitialRTO: time.Second, MaxRTO: time.Second, MaxAttempts: 1})
	gate := make(chan struct{})
	server := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{gate: gate})
	probeDone := make(chan error, 1)
	go func() {
		_, err := client.Probe(context.Background(), server.addr)
		probeDone <- err
	}()
	nextRequest(t, server)
	if err := mux.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-probeDone; !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("probe after mux close error = %v", err)
	}
	if _, err := client.Probe(context.Background(), server.addr); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("new probe after mux close error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestClientCanReattachRegistryAfterClose(t *testing.T) {
	registry, err := NewRegistry(testRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	config := socketmux.DefaultConfig()
	config.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	config.IPv6Policy = socketmux.FamilyDisabled
	config.Classifier = NewClassifier(registry)
	mux, err := socketmux.New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	first, err := NewClient(mux.Control(), registry, fastClientConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := NewClient(mux.Control(), registry, fastClientConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}
