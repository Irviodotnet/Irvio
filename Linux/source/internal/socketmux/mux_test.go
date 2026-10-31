// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestSocketMuxIPv4ReceiveAndSend(t *testing.T) {
	mux := newTestMux(t, ipv4Config())
	destination, ok := mux.LocalAddr(IPv4)
	if !ok || destination.Port() != mux.Port() {
		t.Fatalf("IPv4 local address = %s, active=%v, port=%d", destination, ok, mux.Port())
	}
	sender := listenTestUDP(t, IPv4)
	senderAddress := localAddrPort(t, sender)
	sendInbound(t, sender, destination, []byte("control:in"))
	sendInbound(t, sender, destination, []byte("data:in"))
	control := readLogical(t, mux.Control())
	data := readLogical(t, mux.Data())
	if string(control.Payload) != "control:in" || control.Family != IPv4 || control.Source != senderAddress {
		t.Fatalf("unexpected control datagram: %+v", control)
	}
	if string(data.Payload) != "data:in" || data.Family != IPv4 || data.Source != senderAddress {
		t.Fatalf("unexpected data datagram: %+v", data)
	}

	receiver := listenTestUDP(t, IPv4)
	receiverAddress := localAddrPort(t, receiver)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if n, err := mux.Control().WriteTo(ctx, []byte("control:out"), receiverAddress); err != nil || n != len("control:out") {
		t.Fatalf("control write n=%d err=%v", n, err)
	}
	payload, source := readPhysical(t, receiver)
	if string(payload) != "control:out" || source.Port() != mux.Port() {
		t.Fatalf("control output payload=%q source=%s", payload, source)
	}
	if n, err := mux.Data().WriteTo(ctx, []byte("data:out"), receiverAddress); err != nil || n != len("data:out") {
		t.Fatalf("data write n=%d err=%v", n, err)
	}
	payload, secondSource := readPhysical(t, receiver)
	if string(payload) != "data:out" || secondSource.Port() != mux.Port() || secondSource.Port() != source.Port() {
		t.Fatalf("data output payload=%q source=%s first_source=%s", payload, secondSource, source)
	}
	eventually(t, func() bool {
		stats := mux.Stats()
		return stats.IPv4.ReceivedPackets == 2 && stats.IPv4.TransmittedPackets == 2 &&
			stats.ControlEnqueued == 1 && stats.DataEnqueued == 1
	})
}

func TestSocketMuxIPv6ReceiveAndSend(t *testing.T) {
	requireIPv6(t)
	mux := newTestMux(t, ipv6Config())
	destination, ok := mux.LocalAddr(IPv6)
	if !ok || destination.Port() != mux.Port() {
		t.Fatalf("IPv6 local address = %s, active=%v, port=%d", destination, ok, mux.Port())
	}
	v6Only, err := ipv6Only(mux.ipv6.conn)
	if err != nil {
		t.Fatal(err)
	}
	if v6Only != 1 {
		t.Fatalf("IPV6_V6ONLY = %d, want 1", v6Only)
	}

	sender := listenTestUDP(t, IPv6)
	senderAddress := localAddrPort(t, sender)
	sendInbound(t, sender, destination, []byte("control:in6"))
	sendInbound(t, sender, destination, []byte("data:in6"))
	control := readLogical(t, mux.Control())
	data := readLogical(t, mux.Data())
	if string(control.Payload) != "control:in6" || control.Family != IPv6 || control.Source != senderAddress {
		t.Fatalf("unexpected IPv6 control datagram: %+v", control)
	}
	if string(data.Payload) != "data:in6" || data.Family != IPv6 || data.Source != senderAddress {
		t.Fatalf("unexpected IPv6 data datagram: %+v", data)
	}

	receiver := listenTestUDP(t, IPv6)
	receiverAddress := localAddrPort(t, receiver)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := mux.Control().WriteTo(ctx, []byte("control:out6"), receiverAddress); err != nil {
		t.Fatal(err)
	}
	payload, source := readPhysical(t, receiver)
	if string(payload) != "control:out6" || source.Port() != mux.Port() {
		t.Fatalf("IPv6 control output payload=%q source=%s", payload, source)
	}
	if _, err := mux.Data().WriteTo(ctx, []byte("data:out6"), receiverAddress); err != nil {
		t.Fatal(err)
	}
	payload, secondSource := readPhysical(t, receiver)
	if string(payload) != "data:out6" || secondSource.Port() != mux.Port() || secondSource.Port() != source.Port() {
		t.Fatalf("IPv6 data output payload=%q source=%s first_source=%s", payload, secondSource, source)
	}
	stats := mux.Stats()
	if stats.IPv4.ReceivedPackets != 0 || stats.IPv4.TransmittedPackets != 0 {
		t.Fatalf("IPv4 stats changed in IPv6-only test: %+v", stats.IPv4)
	}
}

func TestSocketMuxClassificationAndDropAccounting(t *testing.T) {
	mux := newTestMux(t, ipv4Config())
	destination, _ := mux.LocalAddr(IPv4)
	sender := listenTestUDP(t, IPv4)
	sendInbound(t, sender, destination, []byte("control:yes"))
	sendInbound(t, sender, destination, []byte("data:unknown"))
	sendInbound(t, sender, destination, []byte("drop:policy"))
	if got := string(readLogical(t, mux.Control()).Payload); got != "control:yes" {
		t.Fatalf("control payload = %q", got)
	}
	if got := string(readLogical(t, mux.Data()).Payload); got != "data:unknown" {
		t.Fatalf("data payload = %q", got)
	}
	eventually(t, func() bool { return mux.Stats().ClassifierDrops == 1 })
	if len(mux.controlQueue) != 0 || len(mux.dataQueue) != 0 {
		t.Fatal("explicitly dropped packet reached a logical queue")
	}
	stats := mux.Stats()
	if stats.ControlEnqueued != 1 || stats.DataEnqueued != 1 || stats.IPv4.ReceivedPackets != 3 {
		t.Fatalf("unexpected classification stats: %+v", stats)
	}
}

func TestSocketMuxControlBackpressureDoesNotBlockData(t *testing.T) {
	config := ipv4Config()
	config.ControlQueueCapacity = 1
	config.DataQueueCapacity = 1
	mux := newTestMux(t, config)
	destination, _ := mux.LocalAddr(IPv4)
	sender := listenTestUDP(t, IPv4)
	for index := 0; index < 4; index++ {
		sendInbound(t, sender, destination, []byte("control:fill"))
	}
	sendInbound(t, sender, destination, []byte("data:alive"))
	if got := string(readLogical(t, mux.Data()).Payload); got != "data:alive" {
		t.Fatalf("data payload = %q", got)
	}
	eventually(t, func() bool { return mux.Stats().ControlOverflowDrops >= 3 })
	if got := string(readLogical(t, mux.Control()).Payload); got != "control:fill" {
		t.Fatalf("queued control payload = %q", got)
	}
}

func TestSocketMuxDataBackpressureDoesNotBlockControl(t *testing.T) {
	config := ipv4Config()
	config.ControlQueueCapacity = 1
	config.DataQueueCapacity = 1
	mux := newTestMux(t, config)
	destination, _ := mux.LocalAddr(IPv4)
	sender := listenTestUDP(t, IPv4)
	for index := 0; index < 4; index++ {
		sendInbound(t, sender, destination, []byte("data:fill"))
	}
	sendInbound(t, sender, destination, []byte("control:alive"))
	if got := string(readLogical(t, mux.Control()).Payload); got != "control:alive" {
		t.Fatalf("control payload = %q", got)
	}
	eventually(t, func() bool { return mux.Stats().DataOverflowDrops >= 3 })
	if got := string(readLogical(t, mux.Data()).Payload); got != "data:fill" {
		t.Fatalf("queued data payload = %q", got)
	}
}

func TestSocketMuxPayloadOwnershipSurvivesBufferReuse(t *testing.T) {
	config := ipv4Config()
	config.DataQueueCapacity = 32
	mux := newTestMux(t, config)
	destination, _ := mux.LocalAddr(IPv4)
	sender := listenTestUDP(t, IPv4)
	original := []byte("first-immutable-payload")
	sendInbound(t, sender, destination, original)
	first := readLogical(t, mux.Data())
	for index := byte(0); index < 20; index++ {
		payload := bytes.Repeat([]byte{index}, len(original))
		sendInbound(t, sender, destination, payload)
		_ = readLogical(t, mux.Data())
	}
	if !bytes.Equal(first.Payload, original) {
		t.Fatalf("previously delivered payload mutated: %q", first.Payload)
	}
}

func TestSocketMuxOversizedDatagramIsNeverDeliveredAsPrefix(t *testing.T) {
	config := ipv4Config()
	config.MaxDatagramSize = 8
	mux := newTestMux(t, config)
	destination, _ := mux.LocalAddr(IPv4)
	sender := listenTestUDP(t, IPv4)
	sendInbound(t, sender, destination, bytes.Repeat([]byte("x"), 64))
	sendInbound(t, sender, destination, []byte("valid"))
	datagram := readLogical(t, mux.Data())
	if string(datagram.Payload) != "valid" {
		t.Fatalf("delivered oversized prefix: %q", datagram.Payload)
	}
	eventually(t, func() bool { return mux.Stats().OversizedDrops == 1 })
	if got := mux.Stats().IPv4; got.ReadErrors != 0 || got.ReceivedPackets != 2 {
		t.Fatalf("truncation was not counted as a dropped datagram: %+v", got)
	}
	if len(mux.dataQueue) != 0 || len(mux.controlQueue) != 0 {
		t.Fatal("oversized datagram reached a logical queue")
	}
}

func TestSocketMuxWriteValidationAndUnavailableFamily(t *testing.T) {
	config := ipv4Config()
	config.MaxDatagramSize = 8
	mux := newTestMux(t, config)
	ctx := context.Background()
	if _, err := mux.Control().WriteTo(ctx, nil, netip.AddrPort{}); !errors.Is(err, ErrInvalidDestination) {
		t.Fatalf("invalid destination error = %v", err)
	}
	mapped := netip.AddrPortFrom(netip.MustParseAddr("::ffff:127.0.0.1"), 9999)
	if _, err := mux.Control().WriteTo(ctx, nil, mapped); !errors.Is(err, ErrInvalidDestination) {
		t.Fatalf("mapped destination error = %v", err)
	}
	ipv6Destination := netip.AddrPortFrom(netip.IPv6Loopback(), 9999)
	if _, err := mux.Data().WriteTo(ctx, nil, ipv6Destination); !errors.Is(err, ErrFamilyUnavailable) {
		t.Fatalf("unavailable family error = %v", err)
	}
	validDestination := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 9999)
	if _, err := mux.Data().WriteTo(ctx, make([]byte, 9), validDestination); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized payload error = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := mux.Data().WriteTo(canceled, nil, validDestination); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write error = %v", err)
	}
	if err := mux.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := mux.Control().WriteTo(ctx, nil, validDestination); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed write error = %v", err)
	}
}
