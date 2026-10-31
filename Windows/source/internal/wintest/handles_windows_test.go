// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wintest

import "testing"

func TestResourceGrowthDoesNotInventGrowthWhenThreadsFall(t *testing.T) {
	before := map[string]int{"Thread": 20, "Event": 66, "IRTimer": 26, "WaitCompletionPacket": 18, "File": 17}
	after := map[string]int{"Thread": 19, "Event": 66, "IRTimer": 26, "WaitCompletionPacket": 18, "File": 17}
	if err := ResourceGrowth(before, after); err != nil {
		t.Fatal("all counts flat or lower", err)
	}
	after["File"] += 5
	if err := ResourceGrowth(before, after); err == nil {
		t.Fatal("thread reduction masked file leak")
	}
}
