// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

// All API operations run on the OS thread impersonating the installed non-administrator account.
func TestWindowsNamedPipeProductNonAdminShutdown(t *testing.T) {
	if os.Getenv("IRVIO_PIPE_PRODUCT_IDLE") != "1" {
		t.Skip("real disposable zero-argument product service required")
	}
	token := testLogonToken(t, "IRVIO_PIPE_ALLOWED")
	if token.IsElevated() {
		t.Fatal("non-elevated user required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := withTestToken(token, func() error {
		client, err := Dial(ctx, DefaultSocketPath)
		if err != nil {
			return err
		}
		defer client.Close()
		status, err := client.GetStatus(ctx)
		if err != nil {
			return err
		}
		if status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED || status.ContributionEnabled {
			return fmt.Errorf("expected idle: %v", status)
		}
		bootstrap, err := client.GetBootstrapState(ctx)
		if err != nil {
			return err
		}
		if bootstrap.Phase != localv1.BootstrapPhase_BOOTSTRAP_PHASE_EMPTY {
			return fmt.Errorf("expected empty bootstrap: %v", bootstrap)
		}
		if err := client.Disconnect(ctx); err != nil {
			return err
		}
		return client.Shutdown(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("non-admin, non-elevated token: authenticated product API, IDLE, EMPTY, DISCONNECT, SHUTDOWN PASS")
}
