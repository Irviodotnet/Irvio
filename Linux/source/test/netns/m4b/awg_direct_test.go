// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"testing"
	"time"
)

func TestM6_DirectEncryptedDataPlane(t *testing.T) {
	requirePrivileged(t)
	tests := []struct {
		name string
		v4   bool
		v6   bool
	}{
		{name: "ipv4", v4: true},
		{name: "ipv6", v6: true},
		{name: "dual-stack", v4: true, v6: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lab := newLab(t, "m6-direct-"+test.name)
			topology := lab.buildDirectDualStack()
			lab.installNFT(topology.ClientNamespace, "m6-direct-client", directICEOracleRules(
				topology.ClientIPv4, topology.ServerIPv4A1, topology.ClientIPv6, topology.ServerIPv6A1, test.v4, test.v6))
			lab.installNFT(topology.ServerNamespace, "m6-direct-server", directICEOracleRules(
				topology.ServerIPv4A1, topology.ClientIPv4, topology.ServerIPv6A1, topology.ClientIPv6, test.v4, test.v6))

			responder, responderReady := startM6DirectHelper(
				t, lab, "responder", topology.ServerNamespace,
				topology.ServerIPv4A1, topology.ServerIPv6A1, test.v4, test.v6, "",
			)
			family := "ipv4"
			if !test.v4 {
				family = "ipv6"
			}
			remote := selectSignalAddress(t, responderReady.ListenAddresses, family)
			initiator, initiatorReady := startM6DirectHelper(
				t, lab, "initiator", topology.ClientNamespace,
				topology.ClientIPv4, topology.ClientIPv6, test.v4, test.v6, remote,
			)
			waitDirectAWGReady(t, initiator, responder)
			if err := touch(initiator.awgStartPath); err != nil {
				t.Fatal(err)
			}
			if err := touch(responder.awgStartPath); err != nil {
				t.Fatal(err)
			}
			initiatorResult := waitConnectReport(t, initiator)
			responderResult := waitConnectReport(t, responder)
			assertConnectedReports(t, initiatorResult, responderResult, test.v4, test.v6)
			assertM6AWGResult(t, "initiator", initiatorReady, initiatorResult)
			assertM6AWGResult(t, "responder", responderReady, responderResult)
			client4, client6 := namespaceUDPSockets(t, lab, topology.ClientNamespace)
			server4, server6 := namespaceUDPSockets(t, lab, topology.ServerNamespace)
			want4, want6 := 0, 0
			if test.v4 {
				want4 = 1
			}
			if test.v6 {
				want6 = 1
			}
			if client4 != want4 || client6 != want6 || server4 != want4 || server6 != want6 {
				t.Fatalf("M6 opened extra UDP sockets client=%d/%d server=%d/%d", client4, client6, server4, server6)
			}
			assertDirectOracle(t, lab, topology.ClientNamespace, test.v4, test.v6)
			assertDirectOracle(t, lab, topology.ServerNamespace, test.v4, test.v6)
			if err := touch(initiator.releasePath); err != nil {
				t.Fatal(err)
			}
			if err := touch(responder.releasePath); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := initiator.child.wait(ctx); err != nil {
				t.Fatal(err)
			}
			if err := responder.child.wait(ctx); err != nil {
				t.Fatal(err)
			}
			lab.captureNamespace(topology.ClientNamespace, "m6-direct-client", true)
			lab.captureNamespace(topology.ServerNamespace, "m6-direct-server", true)
		})
	}
}

func waitDirectAWGReady(t *testing.T, processes ...connectChild) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	for _, process := range processes {
		if err := waitForChildFile(ctx, process.child, process.awgReadyPath); err != nil {
			t.Fatal(err)
		}
	}
}
