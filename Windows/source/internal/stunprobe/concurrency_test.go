// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestConcurrentProbesSameAndDifferentIPv4Servers(t *testing.T) {
	_, _, client := newTestStack(t, false, testRegistryConfig(), fastClientConfig())
	shared := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	servers := []*testSTUNServer{
		newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{}),
		newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{}),
		newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{}),
		newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{}),
	}

	const sharedProbes = 12
	errorsChannel := make(chan error, sharedProbes+len(servers))
	var wg sync.WaitGroup
	for range sharedProbes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Probe(context.Background(), shared.addr)
			errorsChannel <- err
		}()
	}
	for _, server := range servers {
		wg.Add(1)
		go func(server netip.AddrPort) {
			defer wg.Done()
			_, err := client.Probe(context.Background(), server)
			errorsChannel <- err
		}(server.addr)
	}
	wg.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	ids := make(map[transactionID]struct{}, sharedProbes)
	for range sharedProbes {
		request := nextRequest(t, shared)
		ids[request.id] = struct{}{}
	}
	if len(ids) != sharedProbes {
		t.Fatalf("shared server saw %d unique transaction IDs, want %d", len(ids), sharedProbes)
	}
	if stats := client.Stats(); stats.ProbesSucceeded != sharedProbes+uint64(len(servers)) {
		t.Fatalf("successful probes = %d", stats.ProbesSucceeded)
	}
}

func TestConcurrentIPv6Probes(t *testing.T) {
	_, mux, client := newTestStack(t, true, testRegistryConfig(), fastClientConfig())
	servers := make([]*testSTUNServer, 6)
	for index := range servers {
		servers[index] = newTestSTUNServer(t, socketmux.IPv6, testServerBehavior{})
	}
	results := make(chan Result, len(servers))
	errorsChannel := make(chan error, len(servers))
	for _, server := range servers {
		go func(server netip.AddrPort) {
			result, err := client.Probe(context.Background(), server)
			results <- result
			errorsChannel <- err
		}(server.addr)
	}
	for range servers {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
		result := <-results
		if result.Family != socketmux.IPv6 || result.Mapped.Port() != mux.Port() {
			t.Fatalf("invalid IPv6 result: %+v", result)
		}
	}
	for _, server := range servers {
		if request := nextRequest(t, server); request.source.Port() != mux.Port() || !request.source.Addr().Is6() {
			t.Fatalf("IPv6 server observed %s", request.source)
		}
	}
}

func TestOutOfOrderResponsesRemainIsolated(t *testing.T) {
	_, _, client := newTestStack(t, false, testRegistryConfig(), Config{InitialRTO: time.Second, MaxRTO: time.Second, MaxAttempts: 1})
	firstGate := make(chan struct{})
	secondGate := make(chan struct{})
	first := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{gate: firstGate})
	second := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{gate: secondGate})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, err := client.Probe(context.Background(), first.addr)
		firstDone <- err
	}()
	go func() {
		_, err := client.Probe(context.Background(), second.addr)
		secondDone <- err
	}()
	nextRequest(t, first)
	nextRequest(t, second)
	close(secondGate)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-firstDone:
		t.Fatalf("first probe completed before its response: %v", err)
	default:
	}
	close(firstGate)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestTimeoutAndCancellationDoNotAffectOtherProbes(t *testing.T) {
	_, _, client := newTestStack(t, false, testRegistryConfig(), Config{InitialRTO: 10 * time.Millisecond, MaxRTO: 10 * time.Millisecond, MaxAttempts: 1})
	timeoutServer := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{dropFirst: 100})
	successServer := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	timeoutDone := make(chan error, 1)
	successDone := make(chan error, 1)
	go func() {
		_, err := client.Probe(context.Background(), timeoutServer.addr)
		timeoutDone <- err
	}()
	go func() {
		_, err := client.Probe(context.Background(), successServer.addr)
		successDone <- err
	}()
	if err := <-successDone; err != nil {
		t.Fatal(err)
	}
	if err := <-timeoutDone; !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout probe error = %v", err)
	}

	cancelServer := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{dropFirst: 100})
	secondSuccess := newTestSTUNServer(t, socketmux.IPv4, testServerBehavior{})
	ctx, cancel := context.WithCancel(context.Background())
	cancelDone := make(chan error, 1)
	go func() {
		_, err := client.Probe(ctx, cancelServer.addr)
		cancelDone <- err
	}()
	nextRequest(t, cancelServer)
	cancel()
	if _, err := client.Probe(context.Background(), secondSuccess.addr); err != nil {
		t.Fatal(err)
	}
	if err := <-cancelDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled probe error = %v", err)
	}
}
