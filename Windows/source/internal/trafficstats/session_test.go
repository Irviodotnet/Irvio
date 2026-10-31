// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package trafficstats

import "testing"

func TestSnapshotAvailabilityAndCounterReset(t *testing.T) {
	s := &Session{value: Snapshot{SessionID: 42, Complete: true, Active: true}}
	s.update(Snapshot{MyUpload: 123, MyValid: true, ClientsValid: true})
	if got := s.Snapshot(); got.SessionID != 42 || !got.Complete || !got.MyValid || got.SharedValid {
		t.Fatal(got)
	}
	s.update(Snapshot{}) // A missing sample must not hide a subsequent reset.
	s.update(Snapshot{MyUpload: 1, MyValid: true})
	if s.Snapshot().Complete {
		t.Fatal("counter reset must mark totals incomplete")
	}
	s.update(Snapshot{})
	if s.Snapshot().MyValid {
		t.Fatal("unavailable must not become zero")
	}
}

func TestStopRetainsFinalTotals(t *testing.T) {
	s := Start(7, true, func() Snapshot {
		return Snapshot{Clients: 3, ClientsValid: true, MyUpload: 12, MyDownload: 56, DownloadValid: true, SharedUpload: 34, MyValid: true, SharedValid: true}
	})
	s.Stop()
	s.Stop()
	got := s.Snapshot()
	if got.Active || got.Clients != 0 || got.MyUpload != 12 || got.MyDownload != 56 || !got.DownloadValid || got.SharedUpload != 34 || got.SessionID != 7 {
		t.Fatal(got)
	}
}

func TestDownloadResetAcrossMissingSample(t *testing.T) {
	s := &Session{value: Snapshot{Complete: true}}
	s.update(Snapshot{MyDownload: 100, DownloadValid: true})
	s.update(Snapshot{})
	s.update(Snapshot{MyDownload: 1, DownloadValid: true})
	if s.Snapshot().Complete {
		t.Fatal("download reset hidden")
	}
}
