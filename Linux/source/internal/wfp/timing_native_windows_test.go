// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"context"
	"errors"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The independent oracle deliberately enumerates every system filter without a provider template.
func globalFilterKeys(h windows.Handle, provider windows.GUID) (keys map[windows.GUID]bool, err error) {
	keys = make(map[windows.GUID]bool)
	var enumeration windows.Handle
	if err = call("FwpmFilterCreateEnumHandle0", uintptr(h), 0, uintptr(unsafe.Pointer(&enumeration))); err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, call("FwpmFilterDestroyEnumHandle0", uintptr(h), uintptr(enumeration))) }()
	total := 0
	for {
		var entries **filter
		var count uint32
		if err = call("FwpmFilterEnum0", uintptr(h), uintptr(enumeration), 64, uintptr(unsafe.Pointer(&entries)), uintptr(unsafe.Pointer(&count))); err != nil {
			release(entries)
			return nil, err
		}
		if count > 64 || (count > 0 && entries == nil) || total+int(count) > 1000000 {
			release(entries)
			return nil, errors.New("invalid global enumeration")
		}
		for _, f := range unsafe.Slice(entries, int(count)) {
			if f == nil {
				release(entries)
				return nil, errors.New("nil global filter")
			}
			if f.Provider != nil && *f.Provider == provider {
				keys[f.Key] = true
			}
		}
		release(entries)
		total += int(count)
		if count < 64 {
			return keys, nil
		}
	}
}

func TestNativeStoreTimingProfile(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	r := nativeRuleFixture(t)
	if err := s.Replace(ctx, []Rule{r}); err != nil {
		t.Fatal(err)
	}
	for cycle := range 10 {
		var rootTime, filterTime, serviceTime time.Duration
		started := time.Now()
		err := s.transaction(ctx, true, func(h windows.Handle) error {
			stage := time.Now()
			if err := s.validateService(); err != nil {
				return err
			}
			serviceTime = time.Since(stage)
			stage = time.Now()
			if _, err := s.inspectRoots(h); err != nil {
				return err
			}
			if _, err := s.rootWeight(h); err != nil {
				return err
			}
			rootTime = time.Since(stage)
			stage = time.Now()
			count := 0
			if err := s.filters(ctx, h, func(*filter) error { count++; return nil }); err != nil {
				return err
			}
			filterTime = time.Since(stage)
			if count != 1 {
				t.Error("unexpected owned filter count", count)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		total := time.Since(started)
		started = time.Now()
		if err := s.Replace(ctx, []Rule{r}); err != nil {
			t.Fatal(err)
		}
		t.Logf("cycle=%d read_us=%d service_us=%d roots_us=%d filters_us=%d other_us=%d replace_us=%d", cycle,
			total.Microseconds(), serviceTime.Microseconds(), rootTime.Microseconds(), filterTime.Microseconds(),
			(total - serviceTime - rootTime - filterTime).Microseconds(), time.Since(started).Microseconds())
	}
}
