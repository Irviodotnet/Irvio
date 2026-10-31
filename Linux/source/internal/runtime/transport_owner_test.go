// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
)

func TestTransportOwnerRetainsOnlyPredecessorUntilCurrentCommit(t *testing.T) {
	planner := transportgen.BindPlannerFunc(func(networkstate.Snapshot) (transportgen.BindPlan, error) {
		return transportgen.BindPlan{IPv4: netip.MustParseAddr("127.0.0.1")}, nil
	})
	config := transportgen.DefaultConfig(planner)
	config.Validation.AllowLoopback = true
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	builder, err := transportgen.NewBuilder(config)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := NewTransportOwner(builder)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := owner.Activate(context.Background(), networkstate.State{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	first := owner.Current()
	if err := owner.Activate(context.Background(), networkstate.State{Generation: 2}); err != nil {
		t.Fatal(err)
	}
	second := owner.Current()
	if first == second || first.Lifecycle() != transportgen.LifecycleReady || second.ID() != 2 {
		t.Fatalf("invalid make-before-break state: first=%d/%d second=%d/%d", first.ID(), first.Lifecycle(), second.ID(), second.Lifecycle())
	}
	if err := owner.Commit(1); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("stale commit error=%v", err)
	}
	if first.Lifecycle() != transportgen.LifecycleReady {
		t.Fatal("stale commit retired the current predecessor")
	}
	if err := owner.Commit(2); err != nil {
		t.Fatal(err)
	}
	if first.Lifecycle() != transportgen.LifecycleClosed || second.Lifecycle() != transportgen.LifecycleReady {
		t.Fatalf("commit did not retire predecessor: first=%d second=%d", first.Lifecycle(), second.Lifecycle())
	}
	if err := owner.Activate(context.Background(), networkstate.State{Generation: 1}); !errors.Is(err, ErrGenerationStale) {
		t.Fatalf("old NetworkGeneration activation error=%v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if second.Lifecycle() != transportgen.LifecycleClosed || owner.Current() != nil {
		t.Fatal("TransportOwner close did not close current generation")
	}
}

func TestTransportOwnerUsesNewPortWhenPredecessorOwnsPreferredPort(t *testing.T) {
	testTransportOwnerPortPolicy(t, false)
}

func TestTransportOwnerServerRebindsSamePortAcrossGenerations(t *testing.T) {
	testTransportOwnerPortPolicy(t, true)
}

func testTransportOwnerPortPolicy(t *testing.T, fixed bool) {
	probe, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	preferred := probe.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	planner := transportgen.BindPlannerFunc(func(networkstate.Snapshot) (transportgen.BindPlan, error) {
		return transportgen.BindPlan{IPv4: netip.MustParseAddr("127.0.0.1")}, nil
	})
	config := transportgen.DefaultConfig(planner)
	config.Port = int(preferred)
	config.Validation.AllowLoopback = true
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	builder, err := transportgen.NewBuilder(config)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := NewTransportOwner(builder)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	owner.requireFixedPort = fixed
	if err := owner.Activate(context.Background(), networkstate.State{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	first := owner.Current()
	if first.Mux().Port() != preferred {
		t.Fatalf("first port=%d want=%d", first.Mux().Port(), preferred)
	}
	if err := owner.Activate(context.Background(), networkstate.State{Generation: 2}); err != nil {
		t.Fatal(err)
	}
	second := owner.Current()
	if fixed {
		if second == first || second.Mux().Port() != preferred || first.Lifecycle() != transportgen.LifecycleClosed {
			t.Fatalf("fixed server port changed: first=%d second=%d/%d", first.Lifecycle(), second.Mux().Port(), second.Lifecycle())
		}
		if err := owner.Activate(context.Background(), networkstate.State{Generation: 3}); err != nil {
			t.Fatal(err)
		}
		if owner.Current().Mux().Port() != preferred || second.Lifecycle() != transportgen.LifecycleClosed {
			t.Fatal("repeated fixed-port rebind did not close predecessor or preserve port")
		}
		return
	}
	if second == first || second.Mux().Port() == preferred || first.Lifecycle() != transportgen.LifecycleReady {
		t.Fatalf("port fallback failed: first=%d/%d second=%d/%d", first.Mux().Port(), first.Lifecycle(), second.Mux().Port(), second.Lifecycle())
	}
}
