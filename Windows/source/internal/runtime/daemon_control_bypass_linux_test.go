// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import "testing"

func TestAcceptedControlBypassRequiredForProtectedExitWithoutPublicRole(t *testing.T) {
	tests := []struct {
		name   string
		config DaemonConfig
		want   bool
	}{
		{name: "protected community Exit", config: DaemonConfig{Exit: true, ProtectedMode: true}, want: true},
		{name: "protected public verifier", config: DaemonConfig{ProtectedMode: true, PublicRole: PublicRoleConfig{Requested: true}}, want: true},
		{name: "protected client only", config: DaemonConfig{ProtectedMode: true}},
		{name: "unprotected Exit", config: DaemonConfig{Exit: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := needsAcceptedControlBypass(test.config); got != test.want {
				t.Fatalf("accepted-control bypass required=%t, want %t", got, test.want)
			}
		})
	}
}

func TestAcceptedControlListenerPortsDeduplicateDualStackAddresses(t *testing.T) {
	ports, err := acceptedControlListenPorts([]string{
		"/ip4/127.0.0.1/tcp/41463/p2p/12D3KooWMRgtTKXdcPTgVDrrevbP8EFLQXAeQEjrTiGkk6b3vn6k",
		"/ip4/192.168.23.2/tcp/41463/p2p/12D3KooWMRgtTKXdcPTgVDrrevbP8EFLQXAeQEjrTiGkk6b3vn6k",
		"/ip6/::1/tcp/46193/p2p/12D3KooWMRgtTKXdcPTgVDrrevbP8EFLQXAeQEjrTiGkk6b3vn6k",
	})
	if err != nil || len(ports) != 2 || ports[0] != 41463 || ports[1] != 46193 {
		t.Fatalf("ports=%v, err=%v", ports, err)
	}
}
