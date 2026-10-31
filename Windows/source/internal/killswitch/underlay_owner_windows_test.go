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
	"slices"
	"sync"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/wfp"
)

type uncertainWFP struct {
	memoryWFP
	failAfterCommit, unreadable, committedError bool
}

func (s *uncertainWFP) Replace(ctx context.Context, rules []wfp.Rule) error {
	if err := s.memoryWFP.Replace(ctx, rules); err != nil {
		return err
	}
	if s.failAfterCommit {
		s.unreadable = true
	}
	if s.committedError {
		return errors.New("injected engine-close error after commit")
	}
	return nil
}

func (s *uncertainWFP) ReplaceInstalled(ctx context.Context, rules []wfp.Rule) error {
	if !s.state.Installed {
		return wfp.ErrNotInstalled
	}
	return s.Replace(ctx, rules)
}

func (s *uncertainWFP) Inspect(ctx context.Context) (wfp.Snapshot, error) {
	if s.unreadable {
		return wfp.Snapshot{}, errors.New("injected post-commit read failure")
	}
	return s.memoryWFP.Inspect(ctx)
}

func underlayFixture(t *testing.T) (*WindowsBackend, *uncertainWFP, Installation, *WindowsNetworkUpdater, *WindowsUnderlayOwner, WindowsBypass) {
	t.Helper()
	c := windowsFixture()
	grant := c.Network.Underlay[0]
	c.Network.Underlay = nil
	s := &uncertainWFP{memoryWFP: memoryWFP{weight: c.Owner.Weight}}
	b, err := newWindowsBackend(c, s)
	if err != nil {
		t.Fatal(err)
	}
	i, _, err := b.Ensure(context.Background(), c.Policy)
	if err != nil {
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
	return b, s, i, u, o, grant
}

func TestWindowsUnderlayOwnershipAndIndependentLanes(t *testing.T) {
	b, s, i, u, o, grant := underlayFixture(t)
	ctx := context.Background()
	for _, attempt := range []func() error{
		func() error { _, err := u.OwnUnderlay(ctx, 1); return err },
		func() error { return u.Update(ctx, b.network) },
		func() error { return u.UpdateUnderlay(ctx, b.network.Application, nil) },
		func() error { _, _, err := b.Ensure(ctx, b.policy); return err },
	} {
		if !errors.Is(attempt(), ErrWindowsUnderlayBusy) {
			t.Fatal("dynamic owner bypassed")
		}
	}
	a, err := o.Acquire(ctx, 1, grant)
	if err != nil {
		t.Fatal(err)
	}
	c, err := o.Acquire(ctx, 1, grant)
	if err != nil || len(b.network.Underlay) != 1 {
		t.Fatal("duplicate endpoint not reference-counted", err)
	}
	if err := u.UpdateTunnel(ctx, 100, nil); err != nil {
		t.Fatal(err)
	}
	exit := &WindowsExitControl{Interface: 100, Local: netip.MustParseAddrPort("10.88.0.1:51822"), Clients: netip.MustParsePrefix("10.88.0.0/24")}
	if err := u.UpdateExitControl(ctx, exit); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil || len(b.network.Underlay) != 1 {
		t.Fatal("shared grant removed", err)
	}
	if err := c.Close(); err != nil || len(b.network.Underlay) != 0 {
		t.Fatal("last grant retained", err)
	}
	if b.network.TunnelLUID != 100 || b.network.ExitControl == nil || *b.network.ExitControl != *exit {
		t.Fatal("unrelated lane overwritten")
	}
	if err := o.Close(); err != nil || !s.state.Installed {
		t.Fatal("owner close removed protection", err)
	}
	if _, err := u.OwnUnderlay(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := i.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Acquire(ctx, 1, grant); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("removed owner acquired", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsUnderlayUncertainAcquireAndClose(t *testing.T) {
	for _, failure := range []string{"before-commit", "after-commit-inspect", "after-commit-close"} {
		t.Run(failure, func(t *testing.T) {
			b, s, _, u, o, grant := underlayFixture(t)
			s.fail, s.failAfterCommit, s.committedError = failure == "before-commit", failure == "after-commit-inspect", failure == "after-commit-close"
			lease, err := o.Acquire(context.Background(), 1, grant)
			if err == nil || lease == nil || len(o.leases) != 1 {
				t.Fatal("uncertain grant lost ownership")
			}
			cause := err
			if _, err := o.Acquire(context.Background(), 1, grant); !errors.Is(err, ErrWindowsUnderlayBusy) || !errors.Is(err, cause) {
				t.Fatal("dirty owner reused or original failure lost", err)
			}
			if err := u.UpdateTunnel(context.Background(), 0, nil); !errors.Is(err, ErrWindowsUnderlayBusy) {
				t.Fatal("uncertain policy reused by another lane", err)
			}
			if err := lease.Close(); err == nil || len(o.leases) != 1 {
				t.Fatal("failed cleanup forgot grant")
			}
			s.fail, s.failAfterCommit, s.unreadable, s.committedError = false, false, false, false
			if err := lease.Close(); err != nil || len(o.leases) != 0 || len(b.network.Underlay) != 0 {
				t.Fatal("cleanup retry failed", err)
			}
			if o.dirty || o.dirtyCause != nil {
				t.Fatal("successful cleanup retained stale failure")
			}
			if err := b.verify(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWindowsUnderlayAdvanceFencesFailedRevocation(t *testing.T) {
	b, s, _, _, o, grant := underlayFixture(t)
	ctx := context.Background()
	old, err := o.Acquire(ctx, 1, grant)
	if err != nil {
		t.Fatal(err)
	}
	s.fail = true
	if err := o.Advance(ctx, 2); err == nil {
		t.Fatal("injected failure hidden")
	}
	if _, err := o.Acquire(ctx, 1, grant); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("old generation accepted", err)
	}
	if _, err := o.Acquire(ctx, 2, grant); !errors.Is(err, ErrWindowsUnderlayBusy) {
		t.Fatal("new generation before revoke", err)
	}
	if err := old.Close(); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal("old close can restore grants", err)
	}
	s.fail = false
	if err := o.Advance(ctx, 2); err != nil || len(b.network.Underlay) != 0 {
		t.Fatal("advance retry", err)
	}
	current, err := o.Acquire(ctx, 2, grant)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil || len(b.network.Underlay) != 1 {
		t.Fatal("old close removed successor", err)
	}
	if err := o.Advance(ctx, 2); err != nil || len(b.network.Underlay) != 1 {
		t.Fatal("duplicate event revoked successor", err)
	}
	if err := o.Advance(ctx, 1); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal(err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsUnderlayOwnerCloseCannotResurrectGrants(t *testing.T) {
	b, s, _, _, o, grant := underlayFixture(t)
	ctx := context.Background()
	first, err := o.Acquire(ctx, 1, grant)
	if err != nil {
		t.Fatal(err)
	}
	grant.Remote = netip.MustParseAddrPort("192.0.2.11:4000")
	if _, err := o.Acquire(ctx, 1, grant); err != nil {
		t.Fatal(err)
	}
	s.failAfterCommit = true
	if err := o.Close(); err == nil {
		t.Fatal("post-commit failure hidden")
	}
	s.failAfterCommit, s.unreadable = false, false
	if err := first.Close(); !errors.Is(err, ErrWindowsUnderlayBusy) {
		t.Fatal("lease close interferes with owner drain", err)
	}
	if len(b.network.Underlay) != 0 || b.underlayOwner != o || len(o.leases) != 2 {
		t.Fatal("failed shutdown lost ownership or restored grants")
	}
	if err := o.Close(); err != nil || b.underlayOwner != nil || !s.state.Installed {
		t.Fatal("shutdown retry", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsUnderlayLimitsValidationAndConcurrency(t *testing.T) {
	b, _, _, _, o, grant := underlayFixture(t)
	ctx := context.Background()
	bad := grant
	bad.Remote = netip.MustParseAddrPort("0.0.0.0:4000")
	if lease, err := o.Acquire(ctx, 1, bad); err == nil || lease != nil || len(o.leases) != 0 {
		t.Fatal("invalid grant acquired")
	}
	var leases []*WindowsUnderlayLease
	for range maxWindowsUnderlayLeases {
		lease, err := o.Acquire(ctx, 1, grant)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	if lease, err := o.Acquire(ctx, 1, grant); err == nil || lease != nil {
		t.Fatal("unbounded grant refs")
	}
	var wg sync.WaitGroup
	for _, lease := range leases {
		wg.Go(func() {
			if err := lease.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(o.leases) != 0 || len(b.network.Underlay) != 0 {
		t.Fatal("concurrent cleanup leaked")
	}
	for n := range 128 {
		grant.Remote = netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, byte(n + 1)}), 4000)
		if _, err := o.Acquire(ctx, 1, grant); err != nil {
			t.Fatal(err)
		}
	}
	before := slices.Clone(b.rules)
	grant.Remote = netip.MustParseAddrPort("192.0.2.200:4000")
	if lease, err := o.Acquire(ctx, 1, grant); err == nil || lease != nil || !slices.Equal(before, b.rules) {
		t.Fatal("endpoint capacity changed policy")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsUnderlayCommittedCloseErrorFencesOtherLanes(t *testing.T) {
	b, s, installation, u, o, grant := underlayFixture(t)
	ctx := context.Background()
	lease, err := o.Acquire(ctx, 1, grant)
	if err != nil {
		t.Fatal(err)
	}
	s.committedError = true
	if err := o.Close(); err == nil {
		t.Fatal("native engine-close failure hidden")
	}
	s.committedError = false
	if len(b.network.Underlay) != 1 {
		t.Fatal("fixture did not retain pre-commit cached state")
	}
	if err := u.UpdateTunnel(ctx, 0, nil); !errors.Is(err, ErrWindowsUnderlayBusy) {
		t.Fatal("cached underlay republished", err)
	}
	if err := u.UpdateExitControl(ctx, nil); !errors.Is(err, ErrWindowsUnderlayBusy) {
		t.Fatal("Exit lane republished cached grants", err)
	}
	if err := installation.Reconcile(ctx); !errors.Is(err, ErrWindowsUnderlayBusy) {
		t.Fatal("reconcile republished cached grants", err)
	}
	if err := lease.Close(); !errors.Is(err, ErrWindowsUnderlayBusy) {
		t.Fatal("lease republished cached grants", err)
	}
	want := cloneWindowsNetwork(b.network)
	want.Underlay = nil
	rules, err := compileWindowsPolicy(b.owner, want)
	if err != nil || !slices.Equal(s.state.Rules, rules) {
		t.Fatal("kernel grant reappeared", err)
	}
	if err := o.Close(); err != nil || len(b.network.Underlay) != 0 {
		t.Fatal("owner retry", err)
	}
	if err := u.UpdateTunnel(ctx, 0, nil); err != nil {
		t.Fatal("clean owner retained lane lock", err)
	}
	if err := installation.Reconcile(ctx); err != nil {
		t.Fatal("clean owner retained reconcile lock", err)
	}
}
