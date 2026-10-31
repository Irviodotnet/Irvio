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

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
)

func TestPrivateEnrollmentRequiresImportBootstrapAndIdleOrRunningNode(t *testing.T) {
	host := privateLifecycleController(t)
	client := privateLifecycleController(t)
	defer host.Close(context.Background())
	defer client.Close(context.Background())
	ctx := context.Background()
	if _, err := client.ResumePrivateEnrollment(ctx, privateLifecyclePeer(t)); err == nil {
		t.Fatal("unknown target started discovery")
	}
	link, err := host.CreatePrivateInvitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, err := privateaccess.DecodeInvitation(link, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ImportPrivateInvitation(ctx, link, "friend"); err != nil {
		t.Fatal(err)
	}
	client.recoveryPending = true
	if _, err := client.ResumePrivateEnrollment(ctx, token.Host); err == nil {
		t.Fatal("enrollment replaced pending recovery")
	}
	client.recoveryPending = false
	manager, err := productbootstrap.New(productbootstrap.Config{DataDir: client.dataDir, Logger: client.logger})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client.bootstrap = manager
	if _, err := client.ResumePrivateEnrollment(ctx, token.Host); err == nil {
		t.Fatal("missing bootstrap silently bypassed")
	}
	if !client.idleWithoutIntent() || client.daemon != nil {
		t.Fatal("failed enrollment enabled network participation")
	}
	view, err := client.PrivateManagement(ctx)
	if err != nil || len(view.Enrollments) != 1 || len(view.Targets) != 0 {
		t.Fatal("failed enrollment promoted or lost target")
	}
}
