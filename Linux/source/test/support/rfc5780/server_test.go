// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rfc5780

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/natprobe"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

func TestServerProfilesThroughProductionStack(t *testing.T) {
	server := newLoopbackServer(t, Script{})
	registry, err := stunprobe.NewRegistry(stunprobe.RegistryConfig{MaxInFlight: 16, MaxRetired: 32, RetiredGrace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	muxConfig.Classifier = stunprobe.NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := stunprobe.NewClient(mux.Control(), registry, stunprobe.Config{
		InitialRTO: 20 * time.Millisecond, MaxRTO: 40 * time.Millisecond, MaxAttempts: 3,
	})
	if err != nil {
		_ = mux.Close()
		t.Fatal(err)
	}
	profiler, err := natprobe.New(client, natprobe.Config{MinimumTransactionInterval: time.Millisecond, MaxObservations: 16})
	if err != nil {
		_ = client.Close()
		_ = mux.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = profiler.Close()
		_ = client.Close()
		_ = mux.Close()
	})

	profile, err := profiler.Profile(context.Background(), server.Topology().Primary)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Status != natprobe.ProfileComplete || profile.Mapping != natprobe.MappingEndpointIndependent ||
		profile.Filtering != natprobe.FilteringEndpointIndependent || profile.Translation != natprobe.TranslationAbsent {
		t.Fatalf("unexpected real-socket profile: %+v", profile)
	}
	observations, err := server.WaitForObservations(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if observations[0].ChangeRequest != ChangeNone || observations[1].ChangeRequest != ChangeIPAndPort {
		t.Fatalf("unexpected request order: %+v", observations)
	}
	for _, observation := range observations {
		if observation.Source.Port() != mux.Port() {
			t.Fatalf("responder observed port %d, want mux %d", observation.Source.Port(), mux.Port())
		}
	}
}

func TestServerDropsFirstRequestDeterministically(t *testing.T) {
	server := newLoopbackServer(t, Script{DropFirst: 1})
	registry, err := stunprobe.NewRegistry(stunprobe.RegistryConfig{MaxInFlight: 4, MaxRetired: 8, RetiredGrace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.IPv4Bind = netip.MustParseAddr("127.0.0.1")
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	muxConfig.Classifier = stunprobe.NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		t.Fatal(err)
	}
	client, err := stunprobe.NewClient(mux.Control(), registry, stunprobe.Config{
		InitialRTO: 10 * time.Millisecond, MaxRTO: 20 * time.Millisecond, MaxAttempts: 3,
	})
	if err != nil {
		_ = mux.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = mux.Close() })
	result, err := client.Probe(context.Background(), server.Topology().Primary)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempts != 2 || client.Stats().Retransmissions != 1 {
		t.Fatalf("unexpected retry result=%+v stats=%+v", result, client.Stats())
	}
	observations, err := server.WaitForObservations(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if observations[0].TransactionID != observations[1].TransactionID || observations[0].Outcome != "dropped_by_script" {
		t.Fatalf("unexpected retry observations: %+v", observations)
	}
}

func newLoopbackServer(t *testing.T, script Script) *Server {
	t.Helper()
	a1 := netip.MustParseAddr("127.0.0.1")
	a2 := netip.MustParseAddr("127.0.0.2")
	for attempt := 0; attempt < 16; attempt++ {
		p1 := availablePort(t, a1)
		p2 := availablePort(t, a1)
		if p1 == p2 {
			continue
		}
		server, err := New(Config{PrimaryIP: a1, OtherIP: a2, PrimaryPort: p1, OtherPort: p2, Script: script})
		if err == nil {
			t.Cleanup(func() { _ = server.Close() })
			return server
		}
	}
	t.Fatal("could not bind a four-socket loopback responder")
	return nil
}

func availablePort(t *testing.T, address netip.Addr) uint16 {
	t.Helper()
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, 0)))
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
