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
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestM4BEmergencyCleanupRecognizesOwnedNamespaceNames(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate cleanup policy test")
	}
	scriptPath := filepath.Join(filepath.Dir(filename), "..", "..", "..", "scripts", "netns", "cleanup_m4b.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "namespace_pattern='"
	var expression string
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, "'") {
			expression = strings.TrimSuffix(strings.TrimPrefix(line, prefix), "'")
			break
		}
	}
	if expression == "" {
		t.Fatal("namespace_pattern is missing from emergency cleanup script")
	}
	pattern, err := regexp.Compile(expression)
	if err != nil {
		t.Fatalf("compile namespace_pattern: %v", err)
	}

	for _, suffix := range []string{
		"a", "b", "c", "client", "client1", "client2", "client3", "client-b", "client-b2",
		"exit", "exit-a", "exit-b", "exit-c", "exit-d", "internet", "internet-a", "internet-d",
		"server", "switch", "router", "router1", "router2", "probe", "nat", "subject", "subject6",
		"verifier", "verifier6", "wan1", "wan2",
	} {
		name := "hm4b-0123abcd-123-" + suffix
		if !pattern.MatchString(name) {
			t.Errorf("cleanup policy rejects owned namespace %q", name)
		}
	}
	for _, name := range []string{
		"hm4b-0123abcd-123-client-b3",
		"hm4b-0123abcd-123-arbitrary",
		"hm4b-0123abcd-123-client/../../host",
		"other-0123abcd-123-client",
	} {
		if pattern.MatchString(name) {
			t.Errorf("cleanup policy accepts unexpected namespace %q", name)
		}
	}
}
