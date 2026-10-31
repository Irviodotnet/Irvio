// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package scalemodel

import "testing"

func TestModelIsDeterministicAndReplicationImprovesAvailability(t *testing.T) {
	base := Config{Exits: 10_000, Clients: 10_000, Servers: 100, Candidates: 5, Failure: .5, Seed: 0x48594452}
	base.Registrations = 1
	one, err := Run(base)
	if err != nil {
		t.Fatal(err)
	}
	repeated, _ := Run(base)
	if one != repeated {
		t.Fatalf("same seed produced different results: first=%+v second=%+v", one, repeated)
	}
	base.Registrations = 3
	three, err := Run(base)
	if err != nil {
		t.Fatal(err)
	}
	if three.SuccessfulClients <= one.SuccessfulClients || three.CandidateChecks > int64(base.Clients*base.Candidates) {
		t.Fatalf("replication result one=%+v three=%+v", one, three)
	}
}

func TestProgressiveAcquisitionUsesTopUpOnlyAfterInitialFiveMiss(t *testing.T) {
	config := Config{
		Exits: 100, Clients: 1_000, Servers: 10, Registrations: 1,
		Candidates: 10, Progressive: true, PoolLabel: "rare", Failure: .9, Seed: 42,
	}
	result, err := Run(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.LogicalGETs < int64(config.Clients) || result.LogicalGETs > int64(2*config.Clients) ||
		result.CandidateChecks > int64(config.Clients*config.Candidates) {
		t.Fatalf("progressive bounds violated: %+v", result)
	}
}
