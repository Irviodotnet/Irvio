// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/libp2p/go-libp2p/core/peer"
)

func resourceLease(id byte) tunnellease.Lease {
	return tunnellease.Lease{ID: tunnellease.ID{id}, Session: tunnellease.SessionToken{id}, ClientPeer: peer.ID(fmt.Sprint("client-", id)), ExitPeer: peer.ID("exit"), Generation: 1, IPv4: netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 17, 0, id}), 32), ValidUntil: time.Now().Add(time.Hour)}
}

func TestLeaseResourcesCoreLifecycle(t *testing.T) {
	f := newBackendFixture(t, noEmit(t))
	installed, removed := 0, 0
	r, err := NewLeaseResources(f.b, func(context.Context, tunnellease.Lease) error { installed++; return nil }, func(context.Context, tunnellease.Lease) error { removed++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	c := tunnellease.DefaultConfig(peer.ID("exit"))
	c.IPv4Pool = netip.MustParsePrefix("10.17.0.0/24")
	c.IPv6Pool = netip.Prefix{}
	a, err := tunnellease.New(c)
	if err != nil {
		t.Fatal(err)
	}
	lc := tunnellease.DefaultLifecycleConfig(a, r)
	lc.Activator, lc.Renewer, lc.RequireSessionToken = r, r, true
	lifecycle, err := tunnellease.NewLifecycle(lc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lifecycle.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	var leases []tunnellease.Lease
	for id := byte(2); id < 7; id++ {
		v, err := lifecycle.AllocateProvisionalForSession(peer.ID(fmt.Sprint("client-", id)), tunnellease.SessionToken{id}, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		packet := wire(UDP, v.IPv4.Addr(), remote, 30000, 443, 0, nil)
		if err := r.Outbound(context.Background(), packet); !errors.Is(err, ErrStale) {
			t.Fatal("provisional traffic escaped", err)
		}
		v, err = lifecycle.ActivateSession(v.ClientPeer, v.ID, v.Generation, v.Session)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Outbound(context.Background(), packet); err != nil {
			t.Fatal(err)
		}
		leases = append(leases, v)
	}
	old := leases[0]
	renewed, err := lifecycle.RenewSession(old.ClientPeer, old.ID, old.Generation, old.Session)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RevokeLease(context.Background(), old, tunnellease.RevokeExplicitRelease); !errors.Is(err, ErrStale) {
		t.Fatal("old revision removed renewed peer", err)
	}
	if err := lifecycle.ReleaseSession(context.Background(), renewed.ClientPeer, renewed.ID, renewed.Generation, renewed.Session); err != nil {
		t.Fatal(err)
	}
	if err := r.Outbound(context.Background(), wire(UDP, renewed.IPv4.Addr(), remote, 30000, 443, 0, nil)); !errors.Is(err, ErrStale) {
		t.Fatal("released lease still forwards", err)
	}
	for _, v := range leases[1:] {
		if err := r.Outbound(context.Background(), wire(UDP, v.IPv4.Addr(), remote, 30000, 443, 0, nil)); err != nil {
			t.Fatal("unrelated peer lost", err)
		}
	}
	if installed != 5 || removed != 1 || f.b.Stats().Leases != 4 {
		t.Fatal(installed, removed, f.b.Stats())
	}
	if err := lifecycle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if removed != 5 || f.b.Stats().Leases != 0 {
		t.Fatal(removed, f.b.Stats())
	}
}

func TestLeaseResourcesFailedPeerCleanupStaysBounded(t *testing.T) {
	f := newBackendFixture(t, noEmit(t))
	failure := errors.New("peer cleanup failed")
	removeFail := true
	r, err := NewLeaseResources(f.b, func(context.Context, tunnellease.Lease) error { return failure }, func(context.Context, tunnellease.Lease) error {
		if removeFail {
			return failure
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r.limit = 1
	t.Cleanup(func() {
		removeFail = false
		if err := r.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	v := resourceLease(2)
	if err := r.ActivateLease(context.Background(), v); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(r.byID) != 1 || f.b.Stats().Leases != 0 {
		t.Fatal("failed peer lost or NAT remained active")
	}
	if err := r.Outbound(context.Background(), wire(UDP, v.IPv4.Addr(), remote, 30000, 443, 0, nil)); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if err := r.ActivateLease(context.Background(), resourceLease(3)); !errors.Is(err, ErrCapacity) {
		t.Fatal("failed peer escaped capacity", err)
	}
	if err := r.Close(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	removeFail = false
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.byID) != 0 || len(r.bySource) != 0 {
		t.Fatal("retry did not clean peer state")
	}
}

func TestLeaseResourcesCancelDuringPeerInstall(t *testing.T) {
	f := newBackendFixture(t, noEmit(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	removed := 0
	r, err := NewLeaseResources(f.b, func(context.Context, tunnellease.Lease) error { cancel(); return nil }, func(context.Context, tunnellease.Lease) error { removed++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := r.ActivateLease(ctx, resourceLease(2)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if removed != 1 || len(r.byID) != 0 || f.b.Stats().Leases != 0 {
		t.Fatal("cancelled activation remained active")
	}
}

func TestLeaseRevokeCanDrainAWGPlaintextWorker(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint("shutdown=", shutdown), func(t *testing.T) {
			f := newBackendFixture(t, noEmit(t))
			var resources *LeaseResources
			remove := func(ctx context.Context, v tunnellease.Lease) error {
				drained := make(chan error, 1)
				go func() { drained <- resources.Outbound(ctx, wire(UDP, v.IPv4.Addr(), remote, 30000, 443, 0, nil)) }()
				select {
				case err := <-drained:
					if !errors.Is(err, ErrStale) {
						return fmt.Errorf("revoked plaintext not fenced: %v", err)
					}
					return nil
				case <-time.After(time.Second):
					return errors.New("AWG worker blocked behind peer-removal lock")
				}
			}
			var err error
			resources, err = NewLeaseResources(f.b, func(context.Context, tunnellease.Lease) error { return nil }, remove)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := resources.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			v := resourceLease(2)
			if err := resources.ActivateLease(context.Background(), v); err != nil {
				t.Fatal(err)
			}
			if shutdown {
				err = resources.Close(context.Background())
			} else {
				err = resources.RevokeLease(context.Background(), v, tunnellease.RevokeExplicitRelease)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLeaseExpiresDuringPeerInstall(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	c := DefaultConfig(Path{1, 9, netip.MustParseAddr("203.0.113.9")}, nil)
	c.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	b, err := NewBackend(c, &fakePacketSender{}, func(context.Context, Path, uint8) (PacketPort, error) {
		t.Error("unexpected port allocation")
		return nil, ErrConfig
	}, noEmit(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	removed := 0
	r, err := NewLeaseResources(b, func(context.Context, tunnellease.Lease) error { clock.Add(int64(2 * time.Hour)); return nil }, func(context.Context, tunnellease.Lease) error { removed++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := r.ActivateLease(context.Background(), resourceLease(2)); !errors.Is(err, ErrStale) {
		t.Fatal("expired resource reported activated", err)
	}
	if removed != 1 || len(r.byID) != 0 || b.Stats().Leases != 0 {
		t.Fatal("expired activation residue")
	}
}
