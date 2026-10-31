// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package networkstate

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsNotifyFunc func(uint16, uintptr, unsafe.Pointer, bool, *windows.Handle) error

var (
	windowsSubscriptions   sync.Map
	windowsSubscriptionID  atomic.Uint64
	windowsNetworkCallback = windows.NewCallback(deliverWindowsNetworkChange)
)

type windowsSubscription struct {
	closeMu       sync.Mutex
	mu            sync.Mutex
	closed        bool
	released      bool
	events        chan struct{}
	errors        chan error
	context       *uint64
	pin           runtime.Pinner
	handles       []windows.Handle
	cancel        func(windows.Handle) error
	stopReconcile chan struct{}
	reconcileDone chan struct{}
}

func newPlatformSubscription(eventBuffer int) (subscription, error) {
	s, err := subscribeWindows(eventBuffer, []windowsNotifyFunc{
		windows.NotifyIpInterfaceChange, windows.NotifyRouteChange2, windows.NotifyUnicastIpAddressChange,
	}, windows.CancelMibChangeNotify2)
	if err != nil {
		return nil, err
	}
	// Some parameter changes (notably IP MTU) do not deliver a native notification.
	s.startReconcile(5 * time.Second)
	return s, nil
}

func (s *windowsSubscription) startReconcile(interval time.Duration) {
	s.stopReconcile, s.reconcileDone = make(chan struct{}), make(chan struct{})
	stop, done := s.stopReconcile, s.reconcileDone
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.notify()
			}
		}
	}()
}

func subscribeWindows(eventBuffer int, registrations []windowsNotifyFunc, cancel func(windows.Handle) error) (*windowsSubscription, error) {
	if eventBuffer < 1 || eventBuffer > 4096 || len(registrations) == 0 || cancel == nil {
		return nil, fmt.Errorf("invalid Windows network subscription configuration")
	}
	s := &windowsSubscription{events: make(chan struct{}, eventBuffer), errors: make(chan error, 1),
		context: new(uint64), cancel: cancel}
	*s.context = windowsSubscriptionID.Add(1)
	s.pin.Pin(s.context)
	windowsSubscriptions.Store(*s.context, s)
	for i, register := range registrations {
		var handle windows.Handle
		err := register(windows.AF_UNSPEC, windowsNetworkCallback, unsafe.Pointer(s.context), false, &handle)
		if handle != 0 {
			s.handles = append(s.handles, handle)
		}
		if err == nil && handle == 0 {
			err = fmt.Errorf("Windows returned an empty notification handle")
		}
		if err != nil {
			return nil, errors.Join(fmt.Errorf("register network notification %d: %w", i, err), s.Close())
		}
	}
	return s, nil
}

func deliverWindowsNetworkChange(context *uint64, _ unsafe.Pointer, _ uint32) uintptr {
	if context != nil {
		if value, ok := windowsSubscriptions.Load(*context); ok {
			value.(*windowsSubscription).notify()
		}
	}
	return 0
}

func (s *windowsSubscription) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		select {
		case s.events <- struct{}{}:
		default:
		}
	}
}

func (s *windowsSubscription) Events() <-chan struct{} { return s.events }
func (s *windowsSubscription) Errors() <-chan error    { return s.errors }

func (s *windowsSubscription) Close() error {
	if s == nil {
		return nil
	}
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.released {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if s.stopReconcile != nil {
		close(s.stopReconcile)
		<-s.reconcileDone
		s.stopReconcile = nil
	}
	var failures []error
	for i, handle := range s.handles {
		if handle == 0 {
			continue
		}
		// Cancel waits for callbacks, so never hold their mutex here.
		if err := s.cancel(handle); err != nil {
			failures = append(failures, fmt.Errorf("cancel network notification: %w", err))
		} else {
			s.handles[i] = 0
		}
	}
	if len(failures) != 0 {
		// Retain the pinned context until cancellation succeeds; stale callbacks must remain safe.
		return errors.Join(failures...)
	}
	windowsSubscriptions.Delete(*s.context)
	s.pin.Unpin()
	s.released = true
	close(s.events)
	close(s.errors)
	return nil
}
