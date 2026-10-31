// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"google.golang.org/protobuf/proto"
)

func TestDesktopPrivateAPIOverUnixSocketAndPaging(t *testing.T) {
	d := privateLifecycleController(t)
	defer d.Close(context.Background())
	config := localapi.DefaultServerConfig(filepath.Join(t.TempDir(), "api", "control.sock"))
	config.Private = d
	server, err := localapi.NewServer(config, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := localapi.Dial(context.Background(), config.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if !client.SupportsPrivateNetwork() {
		t.Fatal("capability missing")
	}
	created, err := client.PrivateCommand(context.Background(), &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION, LifetimeSeconds: 3600})
	if err != nil || !strings.HasPrefix(created.GetInvitationLink(), privateaccess.InvitationPrefix) {
		t.Fatalf("creation: %v", err)
	}
	locator := [32]byte{9}
	for i := 0; i < 40; i++ {
		if err := d.privateStore.SaveTarget(privateaccess.Target{PeerID: privateLifecyclePeer(t).String(), Name: "friend", Locator: hex.EncodeToString(locator[:])}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := client.GetPrivateState(context.Background(), 0, 32)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.GetPrivateState(context.Background(), 32, 32)
	if err != nil {
		t.Fatal(err)
	}
	if first.TargetCount != 40 || len(first.Targets) != 32 || len(second.Targets) != 8 || first.Revision != second.Revision || len(first.Invitations) != 1 {
		t.Fatal("paging lost count or revision")
	}
	encoded, err := proto.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), created.InvitationLink) || strings.Contains(string(encoded), hex.EncodeToString(locator[:])) || len(encoded) >= localapi.MaxFrameSize {
		t.Fatal("private state disclosed secrets or exceeded frame bound")
	}
	if !d.idleWithoutIntent() || d.daemon != nil {
		t.Fatal("local API management enabled network participation")
	}
	_, err = client.PrivateCommand(context.Background(), &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CANCEL_INVITATION, InvitationId: first.Invitations[0].InvitationId})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDesktopStatusReturnsCachedTransitionDuringLifecycle(t *testing.T) {
	d := privateLifecycleController(t)
	d.mu.Lock()
	d.cacheStatus(irvioruntime.Status{State: irvioruntime.StateDisconnecting})
	done := make(chan irvioruntime.Status, 1)
	go func() { done <- d.Status() }()
	select {
	case status := <-done:
		d.mu.Unlock()
		if status.State != irvioruntime.StateDisconnecting {
			t.Fatal("transition snapshot lost")
		}
	case <-time.After(time.Second):
		d.mu.Unlock()
		<-done
		t.Fatal("status polling blocked behind lifecycle")
	}
	if status := d.Status(); status.State != irvioruntime.StateIdle {
		t.Fatal("cached transition outlived completed lifecycle")
	}
}

func TestDesktopAPIRetriesFailedDisconnectCleanup(t *testing.T) {
	d := privateLifecycleController(t)
	defer d.Close(context.Background())
	d.recoveryPending = true
	var cleanups atomic.Int32
	d.cleanupProtection = func(context.Context) error {
		if cleanups.Add(1) == 1 {
			return errors.New("injected cleanup failure")
		}
		return nil
	}
	config := localapi.DefaultServerConfig(filepath.Join(t.TempDir(), "api", "control.sock"))
	server, err := localapi.NewServer(config, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := localapi.Dial(context.Background(), config.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Disconnect(context.Background()); err == nil {
		t.Fatal("failed cleanup reported success")
	}
	status, err := client.GetStatus(context.Background())
	if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTING {
		t.Fatal("failed cleanup lost disconnecting state")
	}
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cleanups.Load() != 2 || !d.idleWithoutIntent() {
		t.Fatal("API idempotence prevented cleanup retry")
	}
}

func TestPrivateStateReadDoesNotCancelDiscoveryOrWaitForLifecycle(t *testing.T) {
	d := privateLifecycleController(t)
	defer d.Close(context.Background())
	canceled := false
	d.discoveryCancel = func() { canceled = true }
	d.discoveryDone = make(chan struct{})
	close(d.discoveryDone)
	if _, err := d.PrivateState(context.Background(), 0, 32); err != nil {
		t.Fatal(err)
	}
	if canceled {
		t.Fatal("status query canceled country discovery")
	}
	d.mu.Lock()
	done := make(chan error, 1)
	go func() { _, err := d.PrivateState(context.Background(), 0, 32); done <- err }()
	select {
	case err := <-done:
		d.mu.Unlock()
		if err == nil {
			t.Fatal("concurrent lifecycle read was not rejected")
		}
	case <-time.After(time.Second):
		d.mu.Unlock()
		<-done
		t.Fatal("status read waited for long lifecycle command")
	}
}
