// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/iceengine"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestRealAWGWirePacketsRemainData(t *testing.T) {
	registryA, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	registryB, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	classifierA := &recordingClassifier{next: iceadapter.NewClassifier(registryA)}
	classifierB := &recordingClassifier{next: iceadapter.NewClassifier(registryB)}
	outer := netip.MustParseAddr("127.0.0.1")
	muxA := newDataMuxWithClassifier(t, outer, false, classifierA)
	muxB := newDataMuxWithClassifier(t, outer, false, classifierB)
	pair := newAWGTestPairWithMuxes(t, muxA, muxB, outer, outer, "10.87.0.1", "10.87.0.2", icesession.NetworkUDP4)
	pair.establishHandshake(t)
	pair.sendBothDirections(t)
	if classifierA.packets.Load() == 0 || classifierB.packets.Load() == 0 {
		t.Fatal("real AWG handshake/transport traffic did not reach the mux classifiers")
	}
	if classifierA.nonData.Load() != 0 || classifierB.nonData.Load() != 0 {
		t.Fatalf("real AWG traffic was stolen from Data: A=%d B=%d", classifierA.nonData.Load(), classifierB.nonData.Load())
	}
}

func TestDataBackpressureDoesNotStarveICEControl(t *testing.T) {
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	address := netip.MustParseAddr("127.0.0.1")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = 0
	muxConfig.IPv4Policy = socketmux.FamilyRequired
	muxConfig.IPv4Bind = address
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	muxConfig.DataQueueCapacity = 1
	muxConfig.ControlQueueCapacity = 8
	muxConfig.Classifier = iceadapter.NewClassifier(registry)
	muxConfig.Logger = logger
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mux.Close() })

	phases := icesession.NewPhaseManager()
	lease, err := phases.Acquire(context.Background(), icesession.ControlPhaseICE)
	if err != nil {
		t.Fatal(err)
	}
	engineConfig := iceengine.DefaultConfig(address)
	engineConfig.Session.Validation.AllowLoopback = true
	engineConfig.Session.Logger = logger
	engineConfig.Logger = logger
	engine, err := iceengine.New(mux, registry, lease, engineConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("mux=%+v engine=%+v", mux.Stats(), engine.Stats())
		}
	})
	session, err := engine.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	bind, err := NewIrvioBind(mux, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bind.Open(mux.Port()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bind.Shutdown() })

	sender, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	local, _ := mux.LocalAddr(socketmux.IPv4)
	for index := 0; index < 256; index++ {
		if _, err := sender.WriteToUDPAddrPort([]byte("bounded-awg-data-pressure"), local); err != nil {
			t.Fatal(err)
		}
		// Drain kernel input in bounded batches while leaving the Data queue full.
		if (index+1)%16 == 0 {
			waitFor(t, time.Second, func() bool { return mux.Stats().IPv4.ReceivedPackets >= uint64(index+1) }, "pressure batch received")
		}
	}
	waitFor(t, time.Second, func() bool { return mux.Stats().DataOverflowDrops > 0 }, "Data queue overflow")

	username := session.LocalDescription().Credentials.Ufrag + ":remoteA"
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest, stun.NewUsername(username))
	if _, err := sender.WriteToUDPAddrPort(request.Raw, local); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		return mux.Stats().ControlEnqueued > 0 && engine.Stats().Adapter.InboundRequestsRouted > 0
	}, "ICE Control routing under Data pressure")
}

func FuzzUnknownPayloadRemainsData(f *testing.F) {
	f.Add([]byte("ordinary encrypted transport payload"), true)
	f.Add([]byte{1, 0, 0, 0, 0x48, 0x59, 0x44, 0x52, 0x41}, false)
	f.Add([]byte{0, 1, 0, 4, 0x21, 0x12, 0xa4, 0x42}, true)
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		f.Fatal(err)
	}
	classifier := iceadapter.NewClassifier(registry)
	f.Fuzz(func(t *testing.T, payload []byte, ipv4 bool) {
		if len(payload) > 4096 {
			t.Skip()
		}
		source := netip.MustParseAddrPort("[2001:db8::1]:51820")
		family := socketmux.IPv6
		if ipv4 {
			source = netip.MustParseAddrPort("198.18.0.1:51820")
			family = socketmux.IPv4
		}
		if got := classifier.Classify(payload, source, family); got != socketmux.Data {
			t.Fatalf("unknown payload classified as %v", got)
		}
	})
}

type recordingClassifier struct {
	next    socketmux.Classifier
	packets atomic.Uint64
	nonData atomic.Uint64
}

func (c *recordingClassifier) Classify(payload []byte, source netip.AddrPort, family socketmux.AddressFamily) socketmux.PacketClass {
	c.packets.Add(1)
	class := c.next.Classify(payload, source, family)
	if class != socketmux.Data {
		c.nonData.Add(1)
	}
	return class
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}
