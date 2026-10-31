// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeReplaceInstalledNeverCreatesProtection(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	r := nativeRuleFixture(t)
	for cycle := range 2 {
		if err := s.ReplaceInstalled(ctx, []Rule{r}); !errors.Is(err, ErrNotInstalled) {
			t.Fatal("absent protection accepted", err)
		}
		state, err := s.Inspect(ctx)
		if err != nil || state.Installed {
			t.Fatal("absent roots recreated", state, err)
		}
		if err := s.Replace(ctx, []Rule{r}); err != nil {
			t.Fatal(err)
		}
		r.Priority = uint64(20 + cycle)
		if err := s.ReplaceInstalled(ctx, []Rule{r}); err != nil {
			t.Fatal(err)
		}
		assertNativeRules(t, s, []Rule{r})
		if err := s.Remove(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeScopedEnumerationMatchesGlobal(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	var rules []Rule
	for range 130 {
		rules = append(rules, nativeRuleFixture(t))
	}
	if err := s.Replace(ctx, rules); err != nil {
		t.Fatal(err)
	}
	err := s.transaction(ctx, true, func(h windows.Handle) error {
		want, err := globalFilterKeys(h, s.config.Provider)
		if err != nil {
			return err
		}
		seen := 0
		err = s.filters(ctx, h, func(f *filter) error {
			if !want[f.Key] {
				t.Error("unexpected or duplicate scoped filter", f.Key)
			}
			delete(want, f.Key)
			seen++
			return nil
		})
		if err != nil {
			return err
		}
		if seen != len(rules) || len(want) != 0 {
			t.Errorf("incomplete enumeration: seen=%d missing=%d", seen, len(want))
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if err := s.filters(cancelled, h, func(*filter) error { t.Error("callback after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
			t.Error("missing cancellation", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeUnexpectedLayerAndMarkerRemainVisible(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	r := nativeRuleFixture(t)
	if err := s.Replace(ctx, []Rule{r}); err != nil {
		t.Fatal(err)
	}
	// Add an owned inbound-transport rule outside the six layers supported by the product policy.
	unusual, _ := windows.GUIDFromString("{5926dfc8-e3cf-4426-a283-dc393f5d0f9d}")
	n, err := makeNative(nativeRuleFixture(t), s.config)
	if err != nil {
		t.Fatal(err)
	}
	n.Filter.Layer = unusual
	add := func() error {
		return s.transaction(ctx, false, func(h windows.Handle) error {
			var id uint64
			err := call("FwpmFilterAdd0", uintptr(h), uintptr(unsafe.Pointer(&n.Filter)), 0, uintptr(unsafe.Pointer(&id)))
			runtime.KeepAlive(n)
			return err
		})
	}
	remove := func() error {
		return s.transaction(ctx, false, func(h windows.Handle) error {
			return call("FwpmFilterDeleteByKey0", uintptr(h), uintptr(unsafe.Pointer(&n.Filter.Key)))
		})
	}
	if err := add(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := remove(); err != nil {
			t.Error(err)
		}
	})
	err = s.transaction(ctx, true, func(h windows.Handle) error {
		found := false
		if err := s.filters(ctx, h, func(f *filter) error {
			if f.Key == n.Filter.Key {
				found = true
			}
			return nil
		}); err != nil {
			return err
		}
		if !found {
			t.Error("unexpected owned layer hidden")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inspect(ctx); err == nil {
		t.Fatal("unsupported native layer considered healthy")
	}
	if err := s.ReplaceInstalled(ctx, []Rule{r}); err == nil {
		t.Fatal("update silently replaced an unreadable prior policy")
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	marker := []byte("foreign-profile-marker")
	n.Filter.Data = bytesBlob(marker)
	if err := add(); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(marker)
	if _, err := s.Inspect(ctx); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign marker hidden", err)
	}
	if err := s.Remove(ctx); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign filter removed", err)
	}
}

func TestNativeBootTimeFilterRemainsVisible(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	r := nativeRuleFixture(t)
	if err := s.Replace(ctx, []Rule{r}); err != nil {
		t.Fatal(err)
	}
	n, err := makeNative(nativeRuleFixture(t), s.config)
	if err != nil {
		t.Fatal(err)
	}
	n.Filter.Layer, _ = windows.GUIDFromString("{5926dfc8-e3cf-4426-a283-dc393f5d0f9d}")
	n.Filter.Flags = 2
	err = s.transaction(ctx, false, func(h windows.Handle) error {
		var id uint64
		err := call("FwpmFilterAdd0", uintptr(h), uintptr(unsafe.Pointer(&n.Filter)), 0, uintptr(unsafe.Pointer(&id)))
		runtime.KeepAlive(n)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.transaction(ctx, true, func(h windows.Handle) error {
		found := false
		if err := s.filters(ctx, h, func(f *filter) error {
			if f.Key == n.Filter.Key {
				found = true
			}
			return nil
		}); err != nil {
			return err
		}
		if !found {
			t.Error("boot-time owned filter hidden")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Inspect(ctx); err == nil {
		t.Fatal("boot-time filter interpreted as ordinary active policy")
	}
	if err := s.Remove(ctx); err != nil {
		t.Fatal(err)
	}
}
