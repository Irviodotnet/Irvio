// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/reachability"
	"github.com/Irviodotnet/Irvio/internal/serviceindex"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func TestRenewAnnouncementIncrementsSequenceWithinEpoch(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	node, err := Start(context.Background(), Config{
		DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	epoch := append([]byte(nil), node.announcement.SessionEpoch...)
	createdAt := node.announcement.CreatedAtUnix
	if err := node.renewAnnouncement(); err != nil {
		t.Fatal(err)
	}
	if node.announcement.Sequence != 2 {
		t.Fatalf("renewed sequence = %d, want 2", node.announcement.Sequence)
	}
	if !bytes.Equal(node.announcement.SessionEpoch, epoch) {
		t.Fatal("renewal changed the process session epoch")
	}
	if node.announcement.CreatedAtUnix < createdAt {
		t.Fatal("renewal moved creation time backwards")
	}
}

func TestParseDHTMode(t *testing.T) {
	for _, valid := range []string{"", "server", "auto"} {
		if _, err := parseDHTMode(valid); err != nil {
			t.Fatalf("valid mode %q rejected: %v", valid, err)
		}
	}
	if _, err := parseDHTMode("invalid"); err == nil {
		t.Fatal("invalid DHT mode accepted")
	}
}

func TestAnnouncementTimingPolicyIsBoundedAndJittered(t *testing.T) {
	if _, err := Start(context.Background(), Config{
		DataDir: discoveryTestDir(t), AnnouncementTTL: defaultAnnouncementTTL + time.Second,
	}); err == nil {
		t.Fatal("announcement lifetime beyond the M12 maximum was accepted")
	}
	minimum := defaultAnnouncementRefresh * 4 / 5
	maximum := defaultAnnouncementRefresh * 6 / 5
	for range 100 {
		delay := announcementRefreshDelay(defaultAnnouncementRefresh)
		if delay < minimum || delay > maximum {
			t.Fatalf("announcement refresh delay %s outside [%s,%s]", delay, minimum, maximum)
		}
	}
}

func TestNodePublishesAndRotatesSignedAWGBinding(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := bytes.Repeat([]byte{7}, 32)
	node, err := Start(context.Background(), Config{
		DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger,
		AWGPublicKey: first, AWGKeyEpoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	local := node.LocalAnnouncement()
	if !bytes.Equal(local.AwgPublicKey, first) || local.AwgKeyEpoch != 1 {
		t.Fatalf("initial AWG binding = epoch %d key %x", local.AwgKeyEpoch, local.AwgPublicKey)
	}
	second := bytes.Repeat([]byte{8}, 32)
	if err := node.UpdateAWGBinding(second, 2); err != nil {
		t.Fatal(err)
	}
	rotated := node.LocalAnnouncement()
	if !bytes.Equal(rotated.AwgPublicKey, second) || rotated.AwgKeyEpoch != 2 || rotated.Sequence <= local.Sequence {
		t.Fatalf("rotated announcement = epoch %d sequence %d", rotated.AwgKeyEpoch, rotated.Sequence)
	}
	if err := node.UpdateAWGBinding(first, 1); err == nil {
		t.Fatal("AWG key rollback accepted")
	}
}

func TestDeferredExitAdvertisementPublishesCapabilityOnlyWhenReady(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	node, err := Start(context.Background(), Config{
		DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger,
		Exit: true, Country: "DE", DeferExitAdvertisement: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	before := node.LocalAnnouncement()
	if before.Capabilities&protocol.CapExit != 0 || node.providerStarted {
		t.Fatal("deferred Exit was visible before runtime readiness")
	}
	if err := node.publishExitCapability(); err != nil {
		t.Fatal(err)
	}
	after := node.LocalAnnouncement()
	if after.Capabilities&protocol.CapExit == 0 || after.Sequence <= before.Sequence || node.providerStarted {
		t.Fatalf("ready Exit announcement = capability %d sequence %d provider=%v", after.Capabilities, after.Sequence, node.providerStarted)
	}
	if err := protocol.ValidateAnnouncement(after, node.PeerID(), time.Now()); err != nil {
		t.Fatalf("ready Exit announcement invalid: %v", err)
	}
}

func TestDeferredExitCountryUnknownStaysUnadvertisedUntilResolved(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	node, err := Start(context.Background(), Config{
		DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger,
		Exit: true, DeferExitAdvertisement: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if announcement := node.LocalAnnouncement(); announcement.Country != "" || announcement.Capabilities&protocol.CapExit != 0 {
		t.Fatalf("unknown country published Exit: %+v", announcement)
	}
	if err := node.ActivateExit(context.Background()); err == nil {
		t.Fatal("unknown country activated Exit")
	}
	if err := node.SetExitCountry("NL"); err != nil {
		t.Fatal(err)
	}
	if err := node.ActivateExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	announcement := node.LocalAnnouncement()
	if announcement.Country != "NL" || announcement.Capabilities&protocol.CapExit == 0 {
		t.Fatalf("resolved Exit announcement = %+v", announcement)
	}
	if err := node.SetExitCountry("DE"); err == nil {
		t.Fatal("active Exit country changed without withdrawal")
	}
}

func TestDeferredExitCanSuspendAndResumeWithoutProviderLoopGrowth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	node, err := Start(context.Background(), Config{
		DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger,
		Exit: true, Country: "NL", DeferExitAdvertisement: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if err := node.ActivateExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	active := node.LocalAnnouncement()
	if active.Capabilities&protocol.CapExit == 0 || !node.providerLoopStarted {
		t.Fatal("Exit was not activated with one provider lifecycle")
	}
	if !node.ExitActive() {
		t.Fatal("active Exit capability not reflected in node status")
	}
	if err := node.SuspendExit(); err != nil {
		t.Fatal(err)
	}
	suspended := node.LocalAnnouncement()
	if suspended.Capabilities&protocol.CapExit != 0 || suspended.AcceptingNewSessions ||
		suspended.Sequence <= active.Sequence || node.providerStarted {
		t.Fatalf("Exit suspension left an active capability: %+v", suspended)
	}
	if node.ExitActive() {
		t.Fatal("suspended Exit still reported active")
	}
	if err := node.SuspendExit(); err != nil {
		t.Fatalf("idempotent suspension: %v", err)
	}
	if err := node.ActivateExit(context.Background()); err != nil {
		t.Fatal(err)
	}
	resumed := node.LocalAnnouncement()
	if resumed.Capabilities&protocol.CapExit == 0 || resumed.Sequence <= suspended.Sequence ||
		!node.providerStarted || !node.providerLoopStarted {
		t.Fatalf("Exit did not resume the existing provider lifecycle: %+v", resumed)
	}
}

func TestPublicReachabilityUpdateRejectsUnverifiedLocalProof(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	node, err := Start(context.Background(), Config{DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	proof := reachability.Proof{
		Subject: node.PeerID(),
		Endpoint: reachability.Endpoint{
			Family:  irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4,
			Address: netip.MustParseAddrPort("203.0.113.9:45001"), NetworkGeneration: 1, PublicEpoch: 1,
		},
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := node.UpdatePublicReachability([]reachability.Proof{proof}); err == nil {
		t.Fatal("unsigned local public reachability proof was published")
	}
	if announcement := node.LocalAnnouncement(); announcement.Capabilities&(protocol.CapPublicControlV4|protocol.CapPublicControlV6) != 0 ||
		len(announcement.PublicControlEndpoints) != 0 {
		t.Fatalf("invalid proof changed local announcement: %+v", announcement)
	}
}

func TestExitWithoutBootstrapKeepsControlPlaneAvailable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	node, err := Start(context.Background(), Config{
		DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger,
		Exit: true, Country: "RU",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	if node.LocalAnnouncement().Capabilities&protocol.CapExit == 0 {
		t.Fatal("ready Exit capability was not published")
	}
	if !node.providerStarted {
		t.Fatal("Exit reprovide loop was not started")
	}
}

func TestExitAdvertisesAfterJoiningDHT(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	introducer, err := Start(context.Background(), Config{
		DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger, DHTMode: "server",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer introducer.Close()
	exit, err := Start(context.Background(), Config{
		DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger,
		Exit: true, Country: "RU",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer exit.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := exit.Host().Connect(ctx, peer.AddrInfo{
		ID: introducer.PeerID(), Addrs: introducer.Host().Addrs(),
	}); err != nil {
		t.Fatalf("connect to introducer: %v", err)
	}
	if !waitForRoutingPeer(ctx, exit.dht, 5*time.Second) {
		t.Fatal("Exit did not learn an authenticated DHT peer")
	}
	serviceCID, _, err := serviceindex.ExitCID("RU")
	if err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		lookupCtx, lookupCancel := context.WithTimeout(ctx, time.Second)
		found := false
		for provider := range introducer.dht.FindProvidersAsync(lookupCtx, serviceCID, 1) {
			if provider.ID == exit.PeerID() {
				found = true
			}
		}
		lookupCancel()
		if found {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("Exit provider record was not published after authenticated DHT join")
		case <-ticker.C:
		}
	}
}

func TestRendezvousCountryReadvertiseRecoversEmptyRoutingTable(t *testing.T) {
	for _, test := range []struct {
		name            string
		closeConnection bool
	}{
		{name: "connected peer with stale protocol knowledge"},
		{name: "disconnected peer", closeConnection: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			introducer, err := Start(context.Background(), Config{
				DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger, DHTMode: "server",
			})
			if err != nil {
				t.Fatal(err)
			}
			defer introducer.Close()
			var bootstrapAddress string
			for _, address := range introducer.Host().Addrs() {
				if strings.HasPrefix(address.String(), "/ip4/127.0.0.1/tcp/") {
					bootstrapAddress = address.String() + "/p2p/" + introducer.PeerID().String()
					break
				}
			}
			if bootstrapAddress == "" {
				t.Fatal("introducer has no loopback address")
			}
			rendezvousNode, err := Start(context.Background(), Config{
				DataDir: discoveryTestDir(t), ListenPort: 0, Logger: logger,
				DHTMode: "server", Bootstrap: []string{bootstrapAddress},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer rendezvousNode.Close()
			if test.closeConnection {
				if err := rendezvousNode.Host().Network().ClosePeer(introducer.PeerID()); err != nil {
					t.Fatal(err)
				}
			} else {
				if len(rendezvousNode.Host().Network().ConnsToPeer(introducer.PeerID())) == 0 {
					t.Fatal("bootstrap peer is not connected before routing-table loss")
				}
				// Settle Identify, then drop protocol knowledge without closing the authenticated connection.
				time.Sleep(500 * time.Millisecond)
				if err := rendezvousNode.Host().Peerstore().SetProtocols(introducer.PeerID()); err != nil {
					t.Fatal(err)
				}
			}
			rendezvousNode.dht.RoutingTable().RemovePeer(introducer.PeerID())
			if size := rendezvousNode.dht.RoutingTable().Size(); size != 0 {
				t.Fatalf("routing table size after simulated peer loss = %d", size)
			}
			if !test.closeConnection && len(rendezvousNode.Host().Network().ConnsToPeer(introducer.PeerID())) == 0 {
				t.Fatal("simulation must preserve the bootstrap connection")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if err := rendezvousNode.UpdateRendezvousCountries(ctx, []string{"NL"}); err != nil {
				t.Fatalf("re-advertise NL after routing peer loss: %v", err)
			}
			if size := rendezvousNode.dht.RoutingTable().Size(); size == 0 {
				t.Fatal("re-advertise did not restore a routing peer")
			}
		})
	}
}
