// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHelpDoesNotConnectToDaemon(t *testing.T) {
	if option := os.Getenv("IRVIO_TEST_HELP"); option != "" {
		os.Args = []string{"irvio", option}
		if err := run(); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, option := range []string{"--help", "-h"} {
		command := exec.Command(os.Args[0], "-test.run=^TestHelpDoesNotConnectToDaemon$")
		command.Env = append(os.Environ(), "IRVIO_TEST_HELP="+option)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("help failed: %v %s", err, output)
		}
		for _, expected := range []string{"--mode client", "--persistent", "--bootstrap", "irvio status", "irvio stop", "--role server", "sudo ./install.sh", "/opt/irvio/bin/irviod", "bootstrap.png", "close SSH", "journalctl"} {
			if !strings.Contains(string(output), expected) {
				t.Errorf("%s missing %q", option, expected)
			}
		}
	}
}
