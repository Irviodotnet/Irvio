// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
)

func TestSocketMuxCloseUnblocksReadersAndIsIdempotent(t *testing.T) {
	mux := newTestMux(t, ipv4Config())
	results := make(chan error, 2)
	ready := make(chan struct{}, 2)
	for _, endpoint := range []*Endpoint{mux.Control(), mux.Data()} {
		go func(endpoint *Endpoint) {
			ready <- struct{}{}
			_, err := endpoint.Read(context.Background())
			results <- err
		}(endpoint)
	}
	<-ready
	<-ready
	if err := mux.Close(); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		if err := <-results; !errors.Is(err, ErrClosed) {
			t.Fatalf("blocked read error = %v", err)
		}
	}
	for index := 0; index < 10; index++ {
		if err := mux.Close(); err != nil {
			t.Fatalf("repeated close %d: %v", index, err)
		}
	}
	if _, err := mux.Control().Read(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after close error = %v", err)
	}
}

func TestSocketMuxReadHonorsContextCancellation(t *testing.T) {
	mux := newTestMux(t, ipv4Config())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mux.Control().Read(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read error = %v", err)
	}
	if err := mux.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := mux.Control().Read(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read after close error = %v", err)
	}
}

func TestSocketMuxCloseWaitsForActiveWrite(t *testing.T) {
	mux := newTestMux(t, ipv4Config())
	if !mux.beginWrite() {
		t.Fatal("beginWrite rejected an open mux")
	}

	closed := make(chan error, 1)
	go func() {
		closed <- mux.Close()
	}()
	eventually(t, func() bool { return mux.closed.Load() })
	select {
	case err := <-closed:
		t.Fatalf("Close returned before active write completed: %v", err)
	default:
	}

	mux.finishWrite()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestSocketMuxConcurrentLifecycle(t *testing.T) {
	mux := newTestMux(t, ipv4Config())
	receiver := listenTestUDP(t, IPv4)
	destination := localAddrPort(t, receiver)
	start := make(chan struct{})
	errorsChannel := make(chan error, 128)
	var workers sync.WaitGroup

	for index := 0; index < 16; index++ {
		endpoint := mux.Control()
		if index%2 == 1 {
			endpoint = mux.Data()
		}
		workers.Add(1)
		go func(endpoint *Endpoint) {
			defer workers.Done()
			<-start
			_, err := endpoint.Read(context.Background())
			if !errors.Is(err, ErrClosed) {
				errorsChannel <- err
			}
		}(endpoint)
	}
	for index := 0; index < 32; index++ {
		endpoint := mux.Control()
		if index%2 == 1 {
			endpoint = mux.Data()
		}
		workers.Add(1)
		go func(endpoint *Endpoint) {
			defer workers.Done()
			<-start
			_, err := endpoint.WriteTo(context.Background(), []byte("concurrent"), destination)
			if err != nil && !errors.Is(err, ErrClosed) {
				errorsChannel <- err
			}
		}(endpoint)
	}
	for index := 0; index < 8; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 1000; iteration++ {
				_ = mux.Stats()
				_, _ = mux.LocalAddr(IPv4)
			}
		}()
	}
	for index := 0; index < 8; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if err := mux.Close(); err != nil {
				errorsChannel <- err
			}
		}()
	}
	close(start)
	workers.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Error(err)
	}
}

func TestSocketMuxRepeatedConstructionAndDestructionReleasesPort(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		mux, err := New(context.Background(), ipv4Config())
		if err != nil {
			t.Fatalf("construct iteration %d: %v", iteration, err)
		}
		port := mux.Port()
		if err := mux.Close(); err != nil {
			t.Fatalf("close iteration %d: %v", iteration, err)
		}
		probe, err := listenPhysical(context.Background(), IPv4, netip.MustParseAddr("127.0.0.1"), port)
		if err != nil {
			t.Fatalf("port %d was not released at iteration %d: %v", port, iteration, err)
		}
		if err := probe.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
