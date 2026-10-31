// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExitServerConstructionRollbackDoesNotWaitForUnstartedRunLoop(t *testing.T) {
	serverContext, cancel := context.WithCancel(context.Background())
	server := &ExitServer{
		plane: &exitDataPlane{changed: make(chan struct{})},
		ctx:   serverContext, cancel: cancel,
	}
	ctx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	if err := server.closeResources(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("construction rollback waited for an unstarted run loop")
		}
		t.Fatal(err)
	}
}
