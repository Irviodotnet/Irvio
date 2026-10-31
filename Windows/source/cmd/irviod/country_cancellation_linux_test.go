// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"context"
	"testing"
	"time"

	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
)

func TestDesktopCommandCancelsDiscoveryBeforeTakingOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queryDone := make(chan struct{})
	d := &desktopController{discoveryCancel: cancel, discoveryDone: queryDone}
	if d.Status().State != irvioruntime.StateIdle {
		t.Fatal("discovery changed connection intent")
	}
	commandDone := make(chan struct{})
	go func() { d.lockCommand(); d.mu.Unlock(); close(commandDone) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("command did not cancel discovery")
	}
	select {
	case <-commandDone:
		t.Fatal("command raced with discovery cleanup")
	default:
	}
	close(queryDone)
	select {
	case <-commandDone:
	case <-time.After(time.Second):
		t.Fatal("command blocked after discovery cleanup")
	}
}
