// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package trafficstats

import (
	"sync"
	"time"
)

// Snapshot contains only aggregates; validity flags distinguish zero from missing measurements.
type Snapshot struct {
	SessionID                          uint64
	SampledAt                          time.Time
	Clients                            uint32
	MyUpload, MyDownload, SharedUpload uint64
	DownloadValid                      bool
	ClientsValid, MyValid, SharedValid bool
	Complete, Active                   bool
}

type Source func() Snapshot

// Session samples in one worker; Snapshot is I/O-free and Stop retains post-teardown totals.
type Session struct {
	mu                 sync.RWMutex
	value              Snapshot
	stop               chan struct{}
	done               chan struct{}
	once               sync.Once
	lastMy, lastShared uint64
	haveMy, haveShared bool
	lastDownload       uint64
	haveDownload       bool
}

func Start(id uint64, complete bool, source Source) *Session {
	s := &Session{value: Snapshot{SessionID: id, Active: true, Complete: complete}, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			s.update(source())
			select {
			case <-ticker.C:
			case <-s.stop:
				s.update(source())
				s.mu.Lock()
				s.value.Active = false
				s.value.Clients, s.value.ClientsValid = 0, true
				s.mu.Unlock()
				return
			}
		}
	}()
	return s
}

func (s *Session) update(next Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Counter resets mark the session incomplete instead of wrapping or inventing totals.
	if next.MyValid && s.haveMy && next.MyUpload < s.lastMy ||
		next.SharedValid && s.haveShared && next.SharedUpload < s.lastShared ||
		next.DownloadValid && s.haveDownload && next.MyDownload < s.lastDownload {
		s.value.Complete = false
	}
	if next.MyValid {
		s.lastMy, s.haveMy = next.MyUpload, true
	}
	if next.DownloadValid {
		s.lastDownload, s.haveDownload = next.MyDownload, true
	}
	if next.SharedValid {
		s.lastShared, s.haveShared = next.SharedUpload, true
	}
	next.SessionID, next.Active, next.Complete = s.value.SessionID, s.value.Active, s.value.Complete
	next.SampledAt = time.Now().UTC()
	s.value = next
}

func (s *Session) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

func (s *Session) Stop() {
	if s == nil {
		return
	}
	s.once.Do(func() { close(s.stop) })
	<-s.done
}
