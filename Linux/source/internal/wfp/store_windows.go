// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winthread"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

var ErrConflict = errors.New("WFP object is not owned by this policy")
var ErrNotInstalled = errors.New("WFP policy is not installed")

const maxRules = 512

const filterEnumerationBatch = 64
const enumIncludeBootTimeAndDisabled = 0x08 | 0x10

var nativeSlot = make(chan struct{}, 1)

// Config identifies one independently owned policy; ServiceName must refer to its auto-start recovery service.
type Config struct {
	Provider, Sublayer        windows.GUID
	Name, Marker, ServiceName string
	Weight                    uint16
}

type Store struct{ config Config }
type Snapshot struct {
	Installed      bool
	Active         bool
	SublayerWeight uint16
	Rules          []Rule
}

func NewStore(c Config) (*Store, error) {
	if c.Provider == (windows.GUID{}) || c.Sublayer == (windows.GUID{}) || c.Provider == c.Sublayer ||
		c.Name == "" || len(c.Name) > 128 || len(c.Marker) < 8 || len(c.Marker) > 256 ||
		c.ServiceName == "" || len(c.ServiceName) > 256 || c.Weight == 0 ||
		strings.ContainsAny(c.Name+c.Marker+c.ServiceName, "\x00") {
		return nil, fmt.Errorf("invalid WFP ownership configuration")
	}
	return &Store{config: c}, nil
}

// Replace commits the complete ruleset atomically; Close/process exit never removes these persistent objects.
func (s *Store) Replace(ctx context.Context, rules []Rule) error {
	return s.replace(ctx, rules, false)
}

// ReplaceInstalled checks existing ownership inside the write transaction and never creates absent protection.
func (s *Store) ReplaceInstalled(ctx context.Context, rules []Rule) error {
	return s.replace(ctx, rules, true)
}

func (s *Store) replace(ctx context.Context, rules []Rule, requireInstalled bool) error {
	if s == nil || len(rules) == 0 || len(rules) > maxRules {
		return fmt.Errorf("invalid WFP ruleset")
	}
	seen := make(map[windows.GUID]bool)
	native := make([]*nativeRule, 0, len(rules))
	for _, r := range rules {
		if seen[r.Key] {
			return fmt.Errorf("duplicate WFP rule key")
		}
		seen[r.Key] = true
		n, err := makeNative(r, s.config)
		if err != nil {
			return err
		}
		native = append(native, n)
	}
	return s.transaction(ctx, false, func(h windows.Handle) error {
		if err := s.validateService(); err != nil {
			return err
		}
		owned, err := s.inspectRoots(h)
		if err != nil {
			return err
		}
		if owned {
			if err := s.removeFiltersChecked(ctx, h, requireInstalled); err != nil {
				return err
			}
		} else {
			if requireInstalled {
				return ErrNotInstalled
			}
			if err := s.addRoots(h); err != nil {
				return err
			}
		}
		weight, err := s.rootWeight(h)
		if err != nil {
			return err
		}
		if weight != s.config.Weight {
			return fmt.Errorf("WFP sublayer priority unavailable: requested %d, kernel assigned %d", s.config.Weight, weight)
		}
		sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)")
		if err != nil {
			return err
		}
		for _, n := range native {
			if err := ctx.Err(); err != nil {
				return err
			}
			var id uint64
			err = call("FwpmFilterAdd0", uintptr(h), uintptr(unsafe.Pointer(&n.Filter)), uintptr(unsafe.Pointer(sd)), uintptr(unsafe.Pointer(&id)))
			runtime.KeepAlive(n)
			runtime.KeepAlive(sd)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// Inspect reads real native conditions, not merely a metadata fingerprint claiming policy readiness.
func (s *Store) Inspect(ctx context.Context) (Snapshot, error) {
	var result Snapshot
	err := s.transaction(ctx, true, func(h windows.Handle) error {
		owned, err := s.inspectRoots(h)
		if err != nil || !owned {
			return err
		}
		weight, err := s.rootWeight(h)
		if err != nil {
			return err
		}
		result.Installed, result.SublayerWeight, result.Active = true, weight, weight == s.config.Weight
		return s.filters(ctx, h, func(f *filter) error {
			r, err := readRule(f)
			if err != nil {
				return err
			}
			result.Rules = append(result.Rules, r)
			result.Active = result.Active && f.Flags&0x20 == 0
			return nil
		})
	})
	if len(result.Rules) == 0 {
		result.Active = false
	}
	if err != nil {
		return Snapshot{}, err
	}
	return result, err
}

// Remove refuses foreign markers and is idempotent only when all owned roots are already absent.
func (s *Store) Remove(ctx context.Context) error {
	return s.transaction(ctx, false, func(h windows.Handle) error {
		owned, err := s.inspectRoots(h)
		if err != nil || !owned {
			return err
		}
		if err = s.removeFilters(ctx, h); err != nil {
			return err
		}
		if err = call("FwpmSubLayerDeleteByKey0", uintptr(h), uintptr(unsafe.Pointer(&s.config.Sublayer))); err != nil {
			return err
		}
		return call("FwpmProviderDeleteByKey0", uintptr(h), uintptr(unsafe.Pointer(&s.config.Provider)))
	})
}

func (s *Store) transaction(ctx context.Context, readonly bool, fn func(windows.Handle) error) error {
	if s == nil || ctx == nil {
		return fmt.Errorf("WFP store and context required")
	}
	select {
	case nativeSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-nativeSlot }()
	_, err := winthread.Call(func() (struct{}, error) {
		return struct{}{}, s.nativeTransaction(ctx, readonly, fn)
	})
	return err
}

func (s *Store) nativeTransaction(ctx context.Context, readonly bool, fn func(windows.Handle) error) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	h, err := openEngine()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, call("FwpmEngineClose0", uintptr(h))) }()
	var flags uintptr
	if readonly {
		flags = 1
	}
	if err = call("FwpmTransactionBegin0", uintptr(h), flags); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			err = errors.Join(err, call("FwpmTransactionAbort0", uintptr(h)))
		}
	}()
	if err = fn(h); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = call("FwpmTransactionCommit0", uintptr(h)); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) validateService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	svc, err := m.OpenService(s.config.ServiceName)
	if err != nil {
		return fmt.Errorf("WFP recovery service unavailable: %w", err)
	}
	defer svc.Close()
	c, err := svc.Config()
	if err != nil {
		return err
	}
	if c.StartType != mgr.StartAutomatic {
		return fmt.Errorf("WFP recovery service must be automatic for persistent filters to remain enabled after BFE restart")
	}
	return nil
}

func (s *Store) inspectRoots(h windows.Handle) (bool, error) {
	var p *provider
	pe := call("FwpmProviderGetByKey0", uintptr(h), uintptr(unsafe.Pointer(&s.config.Provider)), uintptr(unsafe.Pointer(&p)))
	defer release(p)
	var sl *sublayer
	se := call("FwpmSubLayerGetByKey0", uintptr(h), uintptr(unsafe.Pointer(&s.config.Sublayer)), uintptr(unsafe.Pointer(&sl)))
	defer release(sl)
	pAbsent := errors.Is(pe, windows.Errno(windows.FWP_E_PROVIDER_NOT_FOUND))
	sAbsent := errors.Is(se, windows.Errno(windows.FWP_E_SUBLAYER_NOT_FOUND))
	if pAbsent && sAbsent {
		return false, nil
	}
	if pe != nil && !pAbsent {
		return false, pe
	}
	if se != nil && !sAbsent {
		return false, se
	}
	if pAbsent || sAbsent || p == nil || sl == nil {
		return false, fmt.Errorf("%w: incomplete provider/sublayer roots", ErrConflict)
	}
	pb, err := copyBlob(p.Data)
	if err != nil {
		return false, err
	}
	sb, err := copyBlob(sl.Data)
	if err != nil {
		return false, err
	}
	if p.Key != s.config.Provider || sl.Key != s.config.Sublayer || p.Flags != 1 || sl.Flags != 1 ||
		p.Service == nil || !strings.EqualFold(windows.UTF16PtrToString(p.Service), s.config.ServiceName) ||
		sl.Provider == nil || *sl.Provider != s.config.Provider ||
		!bytes.Equal(pb, []byte(s.config.Marker)) || !bytes.Equal(sb, []byte(s.config.Marker)) {
		return false, fmt.Errorf("%w: provider/sublayer metadata differs (provider_flags=%d sublayer_flags=%d weight=%d service=%q marker_match=%t/%t provider_match=%t)", ErrConflict, p.Flags, sl.Flags, sl.Weight, windows.UTF16PtrToString(p.Service), bytes.Equal(pb, []byte(s.config.Marker)), bytes.Equal(sb, []byte(s.config.Marker)), sl.Provider != nil && *sl.Provider == s.config.Provider)
	}
	return true, nil
}

func (s *Store) rootWeight(h windows.Handle) (uint16, error) {
	var sl *sublayer
	err := call("FwpmSubLayerGetByKey0", uintptr(h), uintptr(unsafe.Pointer(&s.config.Sublayer)), uintptr(unsafe.Pointer(&sl)))
	defer release(sl)
	if err != nil {
		return 0, err
	}
	if sl == nil {
		return 0, fmt.Errorf("missing WFP sublayer")
	}
	return sl.Weight, nil
}

func (s *Store) addRoots(h windows.Handle) error {
	name, _ := windows.UTF16FromString(s.config.Name)
	service, _ := windows.UTF16FromString(s.config.ServiceName)
	marker := []byte(s.config.Marker)
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)")
	if err != nil {
		return err
	}
	p := provider{Key: s.config.Provider, Display: display{Name: &name[0]}, Flags: 1, Data: bytesBlob(marker), Service: &service[0]}
	err = call("FwpmProviderAdd0", uintptr(h), uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(sd)))
	runtime.KeepAlive(p)
	runtime.KeepAlive(name)
	runtime.KeepAlive(service)
	runtime.KeepAlive(marker)
	runtime.KeepAlive(sd)
	if err != nil {
		return err
	}
	sl := sublayer{Key: s.config.Sublayer, Display: display{Name: &name[0]}, Flags: 1, Provider: &s.config.Provider, Data: bytesBlob(marker), Weight: s.config.Weight}
	err = call("FwpmSubLayerAdd0", uintptr(h), uintptr(unsafe.Pointer(&sl)), uintptr(unsafe.Pointer(sd)))
	runtime.KeepAlive(sl)
	runtime.KeepAlive(name)
	runtime.KeepAlive(marker)
	runtime.KeepAlive(sd)
	return err
}

func nativeLayers(ctx context.Context, h windows.Handle) (keys []windows.GUID, err error) {
	var enumeration windows.Handle
	if err = call("FwpmLayerCreateEnumHandle0", uintptr(h), 0, uintptr(unsafe.Pointer(&enumeration))); err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, call("FwpmLayerDestroyEnumHandle0", uintptr(h), uintptr(enumeration))) }()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var entries **layer
		var count uint32
		if err = call("FwpmLayerEnum0", uintptr(h), uintptr(enumeration), filterEnumerationBatch, uintptr(unsafe.Pointer(&entries)), uintptr(unsafe.Pointer(&count))); err != nil {
			release(entries)
			return nil, err
		}
		batchErr := func() error {
			defer release(entries)
			if count > filterEnumerationBatch || (count > 0 && entries == nil) || len(keys)+int(count) > 4096 {
				return fmt.Errorf("WFP layer enumeration exceeds policy bounds")
			}
			for _, item := range unsafe.Slice(entries, int(count)) {
				if item == nil || item.Key == (windows.GUID{}) {
					return fmt.Errorf("invalid native WFP layer")
				}
				keys = append(keys, item.Key)
			}
			return nil
		}()
		if batchErr != nil {
			return nil, batchErr
		}
		if count < filterEnumerationBatch {
			return keys, nil
		}
	}
}

// Enumerate every native layer so unexpected owned filters remain visible to verification and cleanup.
func (s *Store) filters(ctx context.Context, h windows.Handle, fn func(*filter) error) error {
	layers, err := nativeLayers(ctx, h)
	if err != nil {
		return err
	}
	owned := 0
	for _, layer := range layers {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := s.layerFilters(ctx, h, layer, func(f *filter) error {
			owned++
			if owned > maxRules {
				return fmt.Errorf("WFP owned rule bound exceeded")
			}
			return fn(f)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) layerFilters(ctx context.Context, h windows.Handle, layer windows.GUID, fn func(*filter) error) (err error) {
	template := enumTemplate{Provider: &s.config.Provider, Layer: layer, Flags: enumIncludeBootTimeAndDisabled, ActionMask: ^uint32(0)}
	var enumeration windows.Handle
	err = call("FwpmFilterCreateEnumHandle0", uintptr(h), uintptr(unsafe.Pointer(&template)), uintptr(unsafe.Pointer(&enumeration)))
	runtime.KeepAlive(template)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, call("FwpmFilterDestroyEnumHandle0", uintptr(h), uintptr(enumeration))) }()
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var entries **filter
		var count uint32
		if err = call("FwpmFilterEnum0", uintptr(h), uintptr(enumeration), filterEnumerationBatch, uintptr(unsafe.Pointer(&entries)), uintptr(unsafe.Pointer(&count))); err != nil {
			release(entries)
			return err
		}
		batchErr := func() error {
			defer release(entries)
			if count > filterEnumerationBatch || (count > 0 && entries == nil) || total+int(count) > maxRules {
				return fmt.Errorf("WFP enumeration exceeds policy bounds")
			}
			for _, f := range unsafe.Slice(entries, int(count)) {
				if f == nil {
					return fmt.Errorf("nil native WFP entry")
				}
				if f.Provider == nil || *f.Provider != s.config.Provider || f.Layer != layer {
					return fmt.Errorf("%w: unexpected filter in provider/layer enumeration", ErrConflict)
				}
				if f.Sublayer != s.config.Sublayer {
					return fmt.Errorf("%w: filter %s references sublayer %s, expected %s", ErrConflict, f.Key.String(), f.Sublayer.String(), s.config.Sublayer.String())
				}
				data, err := copyBlob(f.Data)
				if err != nil {
					return err
				}
				if !bytes.Equal(data, []byte(s.config.Marker)) {
					return fmt.Errorf("%w: filter owner marker differs", ErrConflict)
				}
				if err = fn(f); err != nil {
					return err
				}
			}
			return nil
		}()
		if batchErr != nil {
			return batchErr
		}
		total += int(count)
		if count < filterEnumerationBatch {
			return nil
		}
	}
}

func (s *Store) removeFilters(ctx context.Context, h windows.Handle) error {
	return s.removeFiltersChecked(ctx, h, false)
}

func (s *Store) removeFiltersChecked(ctx context.Context, h windows.Handle, validate bool) error {
	var keys []windows.GUID
	if err := s.filters(ctx, h, func(f *filter) error {
		if validate {
			if _, err := readRule(f); err != nil {
				return err
			}
		}
		keys = append(keys, f.Key)
		return nil
	}); err != nil {
		return err
	}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := call("FwpmFilterDeleteByKey0", uintptr(h), uintptr(unsafe.Pointer(&key))); err != nil {
			return err
		}
	}
	return nil
}
