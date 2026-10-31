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
	"net/netip"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestProductionTransportUsesExplicitSocketOwnerWithoutFallback(t *testing.T) {
	want := errors.New("platform authorization refused")
	called := 0
	source := netip.MustParseAddr("192.0.2.17")
	state := networkstate.State{Generation: 7, Snapshot: networkstate.Snapshot{
		Links:  []networkstate.Link{{Index: 2, Up: true, Addresses: []netip.Prefix{netip.PrefixFrom(source, 24)}}},
		Routes: []networkstate.Route{{Family: networkstate.FamilyIPv4, LinkIndex: 2, PreferredSource: source}},
	}}
	owner, err := newTransportOwner(ProductionConfig{UDPPort: 51820, RequireFixedUDPPort: true,
		MuxFactory: func(ctx context.Context, got networkstate.State, c socketmux.Config) (*socketmux.Mux, io.Closer, error) {
			called++
			if got.Generation != state.Generation || c.Port != 51820 || c.IPv4Bind != source {
				t.Fatalf("production transport lost platform scope: state=%+v config=%+v", got, c)
			}
			return nil, nil, want
		}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := owner.Activate(context.Background(), state); !errors.Is(err, want) || called != 1 || owner.Current() != nil {
		t.Fatalf("authorization failure bypassed: calls=%d current=%v err=%v", called, owner.Current(), err)
	}
}

func TestLeaseRenewalTimingsScaleWithShortTTL(t *testing.T) {
	lead, retry, err := leaseRenewalTimings(3*time.Second, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if lead != time.Second || retry != 300*time.Millisecond {
		t.Fatalf("scaled renewal timing = %s/%s", lead, retry)
	}
	if _, _, err := leaseRenewalTimings(time.Second, time.Second, 100*time.Millisecond); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("lead at TTL accepted: %v", err)
	}
}
