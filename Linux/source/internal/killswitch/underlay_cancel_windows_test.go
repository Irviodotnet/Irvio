// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/wfp"
)

type cancelGrantStore struct {
	memoryWFP
	stage        string
	cancel       context.CancelFunc
	rollbackFail bool
	triggered    bool
}

func (s *cancelGrantStore) ReplaceInstalled(ctx context.Context, rules []wfp.Rule) error {
	if s.triggered && s.rollbackFail {
		return errors.New("rollback unavailable")
	}
	stage := s.stage
	if stage == "before-commit" {
		s.stage, s.triggered = "", true
		s.cancel()
		return ctx.Err()
	}
	if err := s.memoryWFP.ReplaceInstalled(ctx, rules); err != nil {
		return err
	}
	if stage == "commit-error" || stage == "post-commit-inspect" {
		s.stage, s.triggered = "", true
		s.cancel()
		if stage == "commit-error" {
			return ctx.Err()
		}
	}
	return nil
}

func (s *cancelGrantStore) Inspect(ctx context.Context) (wfp.Snapshot, error) {
	if s.stage == "duplicate-inspect" {
		s.stage, s.triggered = "", true
		s.cancel()
	}
	if err := ctx.Err(); err != nil {
		return wfp.Snapshot{}, err
	}
	return s.memoryWFP.Inspect(ctx)
}

func cancelGrantFixture(t *testing.T) (*WindowsBackend, *cancelGrantStore, *WindowsUnderlayOwner, WindowsBypass) {
	t.Helper()
	c := windowsFixture()
	grant := c.Network.Underlay[0]
	c.Network.Underlay = nil
	s := &cancelGrantStore{memoryWFP: memoryWFP{weight: c.Owner.Weight}}
	b, err := newWindowsBackend(c, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Ensure(context.Background(), c.Policy); err != nil {
		t.Fatal(err)
	}
	u, err := b.NetworkUpdater(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	o, err := u.OwnUnderlay(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return b, s, o, grant
}

func TestWindowsCanceledGrantDoesNotPoisonDatagramStartup(t *testing.T) {
	for _, stage := range []string{"before-commit", "commit-error", "post-commit-inspect", "duplicate-inspect"} {
		t.Run(stage, func(t *testing.T) {
			b, s, owner, grant := cancelGrantFixture(t)
			first, err := owner.Acquire(context.Background(), 1, grant)
			if err != nil {
				t.Fatal(err)
			}
			canceledGrant := grant
			if stage != "duplicate-inspect" {
				canceledGrant.Remote = netip.MustParseAddrPort("192.0.2.20:4000")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.stage, s.cancel = stage, cancel
			lease, err := owner.Acquire(ctx, 1, canceledGrant)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if lease != nil || owner.dirty || len(owner.leases) != 1 {
				t.Fatal("cancelled acquire exposed dirty owner before caller cleanup")
			}
			if err := b.verify(context.Background()); err != nil || len(b.network.Underlay) != 1 || b.network.Underlay[0] != grant {
				t.Fatalf("rollback lost existing protection/peer grant: %v", err)
			}
			udp, err := owner.AcquireDatagramSocket(context.Background(), 1, WindowsDatagramSocket{Interface: grant.Interface, Local: netip.AddrPortFrom(grant.Source, 51820)})
			if err != nil {
				t.Fatalf("unrelated UDP startup rejected: %v", err)
			}
			if err := udp.Close(); err != nil {
				t.Fatal(err)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			if err := owner.Close(); err != nil || !s.state.Installed {
				t.Fatal("cleanup removed protection", err)
			}
		})
	}
}

func TestWindowsCanceledGrantRetainsOwnershipWhenRollbackFails(t *testing.T) {
	b, s, owner, grant := cancelGrantFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.stage, s.cancel, s.rollbackFail = "commit-error", cancel, true
	lease, err := owner.Acquire(ctx, 1, grant)
	if lease == nil || !errors.Is(err, context.Canceled) || !owner.dirty || len(owner.leases) != 1 {
		t.Fatal("uncertain canceled grant lost ownership", err)
	}
	if _, err := owner.AcquireDatagramSocket(context.Background(), 1, WindowsDatagramSocket{Interface: grant.Interface, Local: netip.AddrPortFrom(grant.Source, 51820)}); !errors.Is(err, ErrWindowsUnderlayBusy) {
		t.Fatal("failed rollback bypassed dirty-owner fence", err)
	}
	if err := lease.Close(); err == nil {
		t.Fatal("rollback failure hidden")
	}
	s.rollbackFail = false
	if err := lease.Close(); err != nil || owner.dirty || len(owner.leases) != 0 {
		t.Fatal("retained cleanup retry failed", err)
	}
	if err := b.verify(context.Background()); err != nil || !s.state.Installed {
		t.Fatal("cleanup weakened protection", err)
	}
}

func TestWindowsCanceledAdvanceDoesNotPoisonTeardown(t *testing.T) {
	for _, stage := range []string{"before-commit", "commit-error", "post-commit-inspect"} {
		t.Run(stage, func(t *testing.T) {
			b, s, owner, grant := cancelGrantFixture(t)
			lease, err := owner.Acquire(context.Background(), 1, grant)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.stage, s.cancel = stage, cancel
			if err := owner.Advance(ctx, 2); !errors.Is(err, context.Canceled) {
				t.Fatal("lost canceled generation", err)
			}
			if owner.dirty || owner.draining || owner.generation != 2 || len(owner.leases) != 0 || len(b.network.Underlay) != 0 {
				t.Fatal("canceled migration stranded predecessor grants")
			}
			if err := owner.updater.UpdateTunnel(context.Background(), 0, nil); err != nil {
				t.Fatal("canceled migration blocked tunnel teardown", err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal("retired lease cleanup failed", err)
			}
			if err := b.verify(context.Background()); err != nil || !s.state.Installed {
				t.Fatal("migration cleanup removed protection", err)
			}
		})
	}
}

func TestWindowsCanceledAdvanceRetainsOwnershipOnCleanupFailure(t *testing.T) {
	b, s, owner, grant := cancelGrantFixture(t)
	if _, err := owner.Acquire(context.Background(), 1, grant); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.stage, s.cancel, s.rollbackFail = "commit-error", cancel, true
	if err := owner.Advance(ctx, 2); !errors.Is(err, context.Canceled) || !owner.dirty || !owner.draining {
		t.Fatal("failed cleanup lost fence", err)
	}
	if err := owner.updater.UpdateTunnel(context.Background(), 0, nil); !errors.Is(err, ErrWindowsUnderlayBusy) {
		t.Fatal("uncertain grants republished", err)
	}
	s.rollbackFail = false
	if err := owner.Advance(context.Background(), 2); err != nil || owner.dirty || owner.draining || len(owner.leases) != 0 {
		t.Fatal("retained migration could not retry", err)
	}
	if err := b.verify(context.Background()); err != nil || !s.state.Installed {
		t.Fatal("retry removed protection", err)
	}
}
