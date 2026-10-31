// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/pion/ice/v4"
)

func TestCandidateRoundTrip(t *testing.T) {
	validation := DefaultValidationConfig()
	validation.AllowLoopback = true
	tests := []ice.Candidate{
		mustHost(t, "udp4", "127.0.0.1", 41000, "host4", 1234),
		mustHost(t, "udp6", "::1", 41000, "host6", 2345),
		mustSrflx(t, "udp4", "198.51.100.1", 51001, "10.0.0.2", 41000, "srflx4", 3456),
		mustSrflx(t, "udp6", "2001:db8::10", 51002, "fd00::2", 41000, "srflx6", 4567),
	}
	for _, original := range tests {
		domain, err := CandidateFromPion(original, validation)
		if err != nil {
			t.Fatal(err)
		}
		roundtrip, err := CandidateToPion(domain, validation)
		if err != nil {
			t.Fatal(err)
		}
		if roundtrip.Type() != original.Type() || roundtrip.NetworkType() != original.NetworkType() ||
			roundtrip.Address() != original.Address() || roundtrip.Port() != original.Port() ||
			roundtrip.Foundation() != original.Foundation() || roundtrip.Priority() != original.Priority() ||
			roundtrip.Component() != original.Component() {
			t.Fatalf("candidate roundtrip differs: original=%s result=%s", original.Marshal(), roundtrip.Marshal())
		}
		if original.RelatedAddress() != nil && !roundtrip.RelatedAddress().Equal(original.RelatedAddress()) {
			t.Fatalf("related endpoint changed: %+v != %+v", roundtrip.RelatedAddress(), original.RelatedAddress())
		}
	}
}

func TestDescriptionValidationRejectsUnsafeCandidatesAndCredentials(t *testing.T) {
	validation := DefaultValidationConfig()
	base := Candidate{Type: CandidateTypeHost, Network: NetworkUDP4, Address: netip.MustParseAddr("10.0.0.2"), Port: 41000, Foundation: "f", Priority: 1, Component: 1}
	valid := Description{Credentials: Credentials{Ufrag: "localA", Pwd: strings.Repeat("p", 22)}, Candidates: []Candidate{base}}
	if _, err := ValidateDescription(valid, validation); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Description){
		"short-ufrag":         func(d *Description) { d.Credentials.Ufrag = "abc" },
		"short-password":      func(d *Description) { d.Credentials.Pwd = "short" },
		"whitespace-password": func(d *Description) { d.Credentials.Pwd = strings.Repeat("p", 21) + " " },
		"loopback":            func(d *Description) { d.Candidates[0].Address = netip.MustParseAddr("127.0.0.1") },
		"link-local-v6": func(d *Description) {
			d.Candidates[0].Network = NetworkUDP6
			d.Candidates[0].Address = netip.MustParseAddr("fe80::1")
		},
		"mapped-v6": func(d *Description) {
			d.Candidates[0].Network = NetworkUDP6
			d.Candidates[0].Address = netip.MustParseAddr("::ffff:192.0.2.1")
		},
		"component": func(d *Description) { d.Candidates[0].Component = 2 },
		"duplicate": func(d *Description) { d.Candidates = append(d.Candidates, d.Candidates[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid.Clone()
			mutate(&candidate)
			if _, err := ValidateDescription(candidate, validation); err == nil {
				t.Fatal("invalid description accepted")
			}
		})
	}
}

func mustHost(t *testing.T, network, address string, port int, foundation string, priority uint32) ice.Candidate {
	t.Helper()
	candidate, err := ice.NewCandidateHost(&ice.CandidateHostConfig{
		Network: network, Address: address, Port: port, Component: 1, Foundation: foundation, Priority: priority,
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func mustSrflx(t *testing.T, network, address string, port int, related string, relatedPort int, foundation string, priority uint32) ice.Candidate {
	t.Helper()
	candidate, err := ice.NewCandidateServerReflexive(&ice.CandidateServerReflexiveConfig{
		Network: network, Address: address, Port: port, Component: 1,
		RelAddr: related, RelPort: relatedPort, Foundation: foundation, Priority: priority,
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}
