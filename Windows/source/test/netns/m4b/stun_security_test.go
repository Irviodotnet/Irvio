// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"os"
	"path/filepath"
	"testing"
)

func TestM4B_STUNSecurityAndLifecycleRegression(t *testing.T) {
	requirePrivileged(t)
	lab := newLab(t, "stun-security")
	namespace := lab.namespace("client")
	lab.inNamespace(namespace, "ip", "link", "set", "lo", "up")

	pattern := "^(TestWrongSourceCannotCompleteProbe|" +
		"TestWrongTransactionAndMalformedResponsesRemainData|" +
		"TestUnknownValidAndSTUNLookingMalformedRemainData|" +
		"TestLateResponseIsQuarantined|" +
		"TestDuplicateResponseCannotCompleteTwice|" +
		"TestClientCloseUnblocksProbesAndLeavesMuxUsable|" +
		"TestMuxCloseStopsDispatcherAndActiveProbe)$"
	output := lab.inNamespace(namespace, os.Getenv("HYDRA_M4B_STUNTEST"), "-test.v", "-test.run", pattern, "-test.count=1")
	if err := os.WriteFile(filepath.Join(lab.artifactDir, "stun-security-tests.txt"), []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
}
