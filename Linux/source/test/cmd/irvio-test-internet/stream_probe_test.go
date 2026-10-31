//go:build linux

// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStreamProbeAcceptsOnlyExpectedEgress(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		name := "expected"
		if wrong {
			name = "wrong-egress"
		}
		t.Run(name, func(t *testing.T) {
			server, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			root := t.TempDir()
			config := options{target: server.LocalAddr().String(), expectedEgress: "198.18.56.2", probeTimeout: 200 * time.Millisecond,
				ready: filepath.Join(root, "ready"), release: filepath.Join(root, "stop"), result: filepath.Join(root, "result.json")}
			serverResult := make(chan error, 1)
			go func() {
				_ = server.SetDeadline(time.Now().Add(3 * time.Second))
				for index := 0; index < 2; index++ {
					var packet [256]byte
					_, remote, err := server.ReadFrom(packet[:])
					if err != nil {
						serverResult <- err
						return
					}
					source := "198.18.56.2:12345"
					if wrong {
						source = "198.18.90.2:12345"
					}
					body, _ := json.Marshal(probeResult{ObservedSource: source, Success: true})
					if _, err := server.WriteTo(body, remote); err != nil {
						serverResult <- err
						return
					}
					if wrong {
						serverResult <- nil
						return
					}
				}
				serverResult <- os.WriteFile(config.release, []byte("stop\n"), 0o600)
			}()
			err = streamProbe(config)
			if (err != nil) != wrong {
				t.Fatalf("wrong=%t error=%v", wrong, err)
			}
			if err := <-serverResult; err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(config.result)
			if err != nil {
				t.Fatal(err)
			}
			var report streamProbeReport
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatal(err)
			}
			if wrong {
				if report.WrongEgress != 1 || report.Completed {
					t.Fatalf("wrong egress not rejected: %+v", report)
				}
			} else if !report.Completed || report.Successes < 2 || report.WrongEgress != 0 {
				t.Fatalf("missing successful stream proof: %+v", report)
			}
		})
	}
}

func TestStreamProbeRejectsPrivilegedOrUnboundedConfiguration(t *testing.T) {
	for name, mutate := range map[string]func(*options){
		"mark": func(c *options) { c.socketMark = 1 }, "device": func(c *options) { c.bindDevice = "eth0" },
		"long-timeout": func(c *options) { c.probeTimeout = 2 * time.Second }, "no-timeout": func(c *options) { c.probeTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			config := options{target: "127.0.0.1:1", expectedEgress: "198.18.56.2", probeTimeout: 200 * time.Millisecond,
				ready: filepath.Join(root, "ready"), release: filepath.Join(root, "stop"), result: filepath.Join(root, "result.json")}
			if err := os.WriteFile(config.release, []byte("stop\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			mutate(&config)
			if err := streamProbe(config); err == nil || !strings.Contains(err.Error(), "requires bounded unmarked probes") {
				t.Fatalf("unsafe stream configuration was not rejected by validation: %v", err)
			}
		})
	}
}
