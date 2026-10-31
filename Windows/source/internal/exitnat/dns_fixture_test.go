// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDNSFixtureRetriesAreBounded(t *testing.T) {
	t.Run("lost-request", func(t *testing.T) {
		packets := make(chan []byte, 1)
		retries, err := awaitEncryptedDNS(context.Background(), packets, nil, time.Millisecond, func() error {
			packets <- []byte{1}
			return nil
		}, func(p []byte) (bool, error) { return len(p) == 1 && p[0] == 1, nil })
		if err != nil || retries != 1 {
			t.Fatal(retries, err)
		}
	})
	t.Run("no-response", func(t *testing.T) {
		retries, err := awaitEncryptedDNS(context.Background(), nil, nil, time.Millisecond, func() error { return nil }, nil)
		if !errors.Is(err, context.DeadlineExceeded) || retries != 2 {
			t.Fatal(retries, err)
		}
	})
	t.Run("stale-duplicates-do-not-extend-budget", func(t *testing.T) {
		packets := make(chan []byte, 8)
		for i := 0; i < cap(packets); i++ {
			packets <- []byte{0}
		}
		retries, err := awaitEncryptedDNS(context.Background(), packets, nil, time.Millisecond, func() error { return nil }, func([]byte) (bool, error) { return false, nil })
		if !errors.Is(err, context.DeadlineExceeded) || retries != 2 {
			t.Fatal(retries, err)
		}
	})
	for _, phase := range []string{"backend", "validation", "send"} {
		t.Run(phase+"-error-is-not-retried", func(t *testing.T) {
			want := errors.New(phase + " failure")
			packets, failures := make(chan []byte, 1), make(chan error, 1)
			if phase == "backend" {
				failures <- want
			} else if phase == "validation" {
				packets <- []byte{1}
			}
			retries, err := awaitEncryptedDNS(context.Background(), packets, failures, 10*time.Millisecond, func() error { return want }, func([]byte) (bool, error) { return false, want })
			expected := 0
			if phase == "send" {
				expected = 1
			}
			if !errors.Is(err, want) || retries != expected {
				t.Fatal(retries, err)
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		retries, err := awaitEncryptedDNS(ctx, nil, nil, time.Second, func() error { t.Fatal("retried cancelled exchange"); return nil }, nil)
		if !errors.Is(err, context.Canceled) || retries != 0 {
			t.Fatal(retries, err)
		}
	})
}
