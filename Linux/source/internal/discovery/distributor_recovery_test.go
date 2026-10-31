// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestDistributorRecoveryAddresslessRendezvousCanBecomeDialable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream := make(chan peer.AddrInfo)
	attempted := make(chan struct{})
	go func() {
		defer close(stream)
		select {
		case stream <- peer.AddrInfo{ID: "same-provider"}:
		case <-ctx.Done():
			return
		}
		select {
		case <-attempted:
		case <-ctx.Done():
			return
		}
		select {
		case stream <- peer.AddrInfo{ID: "same-provider", Addrs: []ma.Multiaddr{ma.StringCast("/ip4/127.0.0.1/tcp/4000")}}:
		case <-ctx.Done():
			return
		}
	}()
	result, err := verifyRendezvousProviders(ctx, "self", stream, func(_ context.Context, info peer.AddrInfo) (bool, error) {
		if len(info.Addrs) == 0 {
			close(attempted)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("addressless hint permanently suppresses later dialable hint for same Rendezvous in this lookup; result=%v", result)
	}
}

func TestRendezvousCountryPublicationOnlyOnChangeOrRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, err := Start(ctx, Config{DataDir: discoveryTestDir(t), DHTMode: "server", Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	address, err := first.IPv4LoopbackAddress()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Start(ctx, Config{DataDir: discoveryTestDir(t), DHTMode: "server", Logger: logger, Bootstrap: []string{address}})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = second.UpdateRendezvousCountries(ctx, []string{"NL"}); err != nil {
		t.Fatal(err)
	}
	stamp := second.rendezvousPublished["NL"]
	for i := 0; i < 3; i++ {
		if err = second.UpdateRendezvousCountries(ctx, []string{"NL"}); err != nil {
			t.Fatal(err)
		}
	}
	if second.rendezvousPublished["NL"] != stamp {
		t.Fatal("unchanged country was republished")
	}
	expired := time.Now().Add(-second.providerRefresh)
	second.rendezvousPublished["NL"] = expired
	if err = second.UpdateRendezvousCountries(ctx, []string{"NL"}); err != nil {
		t.Fatal(err)
	}
	// A successful refresh need not advance beyond the same Windows clock tick.
	refreshed := second.rendezvousPublished["NL"]
	if !refreshed.After(expired) || refreshed.Before(stamp) || time.Since(refreshed) >= second.providerRefresh {
		t.Fatalf("periodic reprovide was lost: original=%v expired=%v current=%v", stamp, expired, refreshed)
	}
	if err = second.UpdateRendezvousCountries(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if len(second.rendezvousPublished) != 0 {
		t.Fatal("withdrawn country publication retained")
	}
	if err = second.UpdateRendezvousCountries(ctx, []string{"NL"}); err != nil {
		t.Fatal(err)
	}
	if second.rendezvousPublished["NL"].IsZero() {
		t.Fatal("returning country not republished")
	}
}
