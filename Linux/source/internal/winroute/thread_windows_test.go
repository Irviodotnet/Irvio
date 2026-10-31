// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"testing"

	"github.com/Irviodotnet/Irvio/internal/wintest"
)

func TestNativeRouteWorkerPreservesUnrelatedPendingIO(t *testing.T) {
	if err := wintest.PreservePendingIO(func() error { return routeOperation(nativeRouteBackend{}, func() error { return nil }) }); err != nil {
		t.Fatal(err)
	}
}
