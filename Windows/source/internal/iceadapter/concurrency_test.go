// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestConcurrentStatsGetConnAndClose(t *testing.T) {
	stack := newTestStack(t, false, 8)
	local := stack.adapter.GetListenAddresses()[0]
	var wait sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for iteration := 0; iteration < 20; iteration++ {
				connection, err := stack.adapter.GetConn("localA", local)
				if err == nil {
					_ = connection.Close()
				} else if !errors.Is(err, ErrClosed) {
					t.Errorf("GetConn: %v", err)
					return
				}
				_ = stack.adapter.Stats()
			}
		}()
	}
	close(start)
	if err := stack.adapter.Close(); err != nil {
		t.Fatal(err)
	}
	wait.Wait()
	if stats := stack.adapter.Stats(); stats.ActiveUfrags != 0 || stats.ActiveTransactions != 0 {
		t.Fatalf("registry leaked state after close: %+v", stats)
	}
}

func TestConcurrentWriteAndAdapterClose(t *testing.T) {
	stack := newTestStack(t, false, 8)
	connection, err := stack.adapter.GetConn("localA", stack.adapter.GetListenAddresses()[0])
	if err != nil {
		t.Fatal(err)
	}
	receiver := listenUDP(t, socketmux.IPv4)
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest, stun.NewUsername("remoteA:localA"))

	start := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		<-start
		for {
			_, writeErr := connection.WriteTo(request.Raw, receiver.LocalAddr())
			if writeErr == nil {
				continue
			}
			done <- writeErr
			return
		}
	}()
	close(start)
	if err := stack.adapter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("concurrent WriteTo error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WriteTo remained blocked during adapter close")
	}
	if _, err := connection.WriteTo(request.Raw, receiver.LocalAddr()); !errors.Is(err, ErrClosed) {
		t.Fatalf("WriteTo after adapter close error = %v", err)
	}
	if stats := stack.adapter.Stats(); stats.ActiveUfrags != 0 || stats.ActiveTransactions != 0 {
		t.Fatalf("registry leaked state after concurrent close: %+v", stats)
	}
}
