// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windns

import (
	"testing"

	"github.com/Irviodotnet/Irvio/internal/wintest"
)

func TestNativeDNSWorkerPreservesUnrelatedPendingIO(t *testing.T) {
	if err := wintest.PreservePendingIO(func() error { return nativeDNS(func() error { return nil }) }); err != nil {
		t.Fatal(err)
	}
}
