// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import (
	"net/netip"
	"testing"

	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/icesession"
)

func TestCandidateSnapshotSupportsServerAndPeerReflexiveDiagnostics(t *testing.T) {
	tests := []struct {
		name      string
		candidate ice.Candidate
		wantType  ice.CandidateType
		wantRel   netip.AddrPort
	}{
		{
			name: "host",
			candidate: mustDiagnosticHost(t, &ice.CandidateHostConfig{
				Network: "udp4", Address: "192.0.2.10", Port: 41000, Component: 1,
				Foundation: "host-foundation", Priority: 5678,
			}),
			wantType: ice.CandidateTypeHost,
		},
		{
			name: "server-reflexive",
			candidate: mustDiagnosticSrflx(t, &ice.CandidateServerReflexiveConfig{
				Network: "udp4", Address: "198.51.100.10", Port: 51001, Component: 1,
				RelAddr: "10.51.1.2", RelPort: 41000, Foundation: "srflx-foundation", Priority: 1234,
			}),
			wantType: ice.CandidateTypeServerReflexive,
			wantRel:  netip.MustParseAddrPort("10.51.1.2:41000"),
		},
		{
			name: "peer-reflexive",
			candidate: mustDiagnosticPrflx(t, &ice.CandidatePeerReflexiveConfig{
				Network: "udp4", Address: "198.51.100.20", Port: 52002, Component: 1,
				Foundation: "prflx-foundation", Priority: 4321,
			}),
			wantType: ice.CandidateTypePeerReflexive,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := candidateSnapshot(test.candidate, false)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Type != test.wantType || snapshot.Network != icesession.NetworkUDP4 ||
				snapshot.Address.String() != test.candidate.Address() || snapshot.Port != uint16(test.candidate.Port()) ||
				snapshot.Foundation != test.candidate.Foundation() || snapshot.Priority != test.candidate.Priority() {
				t.Fatalf("snapshot=%+v candidate=%s", snapshot, test.candidate)
			}
			if test.wantRel.IsValid() && netip.AddrPortFrom(snapshot.RelatedAddress, snapshot.RelatedPort) != test.wantRel {
				t.Fatalf("related endpoint=%s want=%s", netip.AddrPortFrom(snapshot.RelatedAddress, snapshot.RelatedPort), test.wantRel)
			}
			_, signaled := snapshot.signaled()
			if signaled != (test.wantType != ice.CandidateTypePeerReflexive) {
				t.Fatalf("signaled=%v type=%s", signaled, test.wantType)
			}
		})
	}
}

func TestConnectedResultDefensivelyCopiesRemoteDiagnostics(t *testing.T) {
	session := &ConnectedSession{result: Result{RemoteCandidates: []CandidateSnapshot{{
		Type: ice.CandidateTypePeerReflexive, Network: icesession.NetworkUDP4,
		Address: netip.MustParseAddr("198.51.100.20"), Port: 52002,
	}}}}
	first := session.Result()
	first.RemoteCandidates[0].Port = 1
	second := session.Result()
	if second.RemoteCandidates[0].Port != 52002 {
		t.Fatalf("stored diagnostics were mutated: %+v", second.RemoteCandidates)
	}
}

func mustDiagnosticHost(t *testing.T, config *ice.CandidateHostConfig) ice.Candidate {
	t.Helper()
	candidate, err := ice.NewCandidateHost(config)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func mustDiagnosticSrflx(t *testing.T, config *ice.CandidateServerReflexiveConfig) ice.Candidate {
	t.Helper()
	candidate, err := ice.NewCandidateServerReflexive(config)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func mustDiagnosticPrflx(t *testing.T, config *ice.CandidatePeerReflexiveConfig) ice.Candidate {
	t.Helper()
	candidate, err := ice.NewCandidatePeerReflexive(config)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}
