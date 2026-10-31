// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"context"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/protocol"
)

func TestPrivateTargetExactIdentityAndPublicIsolation(t *testing.T) {
	now := time.Now()
	local := selectorPeer(t, 1)
	wanted := selectorCandidate(t, 2, now)
	wanted.Country = ""
	wanted.Capabilities = protocol.CapPrivateExit
	wanted.Transport = CandidateTransportBrokered
	wanted.DescriptorID = [32]byte{1}
	wanted.DescriptorIncarnation, wanted.DescriptorSequence = 1, 1
	target := Target{Kind: TargetPrivatePeer, PeerID: wanted.PeerID}
	other := wanted
	other.PeerID = selectorPeer(t, 3)
	public := selectorCandidate(t, 4, now)
	calls := 0
	probe := CandidateProbeFunc(func(_ context.Context, candidate Candidate) (ProbeResult, error) {
		calls++
		if candidate.PeerID != target.PeerID {
			t.Error("unrelated peer reached probe")
		}
		return ProbeResult{Reachable: true, ObservedAt: now, NetworkEpoch: candidate.NetworkEpoch, Kind: ProbeBrokerMetadata}, nil
	})
	config := EvaluationConfig{LocalPeer: local, Target: target, Now: now, ProbeConcurrency: 1, ProbeTimeout: time.Second, LatencyTolerance: time.Millisecond}
	batch := CandidateBatch{Target: target, AvailablePeerCount: 3, GeneratedAt: now, Candidates: []Candidate{other, public, wanted}}
	selection, err := EvaluateBatch(context.Background(), batch, probe, config)
	if err != nil || selection.Active == nil || selection.Active.Candidate.PeerID != target.PeerID || selection.Reserve != nil || selection.Cold != nil || calls != 1 {
		t.Fatalf("private selection escaped pinned identity: %v", err)
	}
	if err := validateCandidate(wanted, "DE", local, now); err == nil {
		t.Fatal("private capability entered public selection")
	}
	for _, country := range []string{"DE", "NL", ""} {
		changed := wanted
		changed.Country = country
		changed.DescriptorSequence++
		batch.Candidates = []Candidate{public, other, changed}
		calls = 0
		selection, err := EvaluateBatch(context.Background(), batch, probe, config)
		if err != nil || selection.Active == nil || selection.Active.Candidate.PeerID != target.PeerID || selection.Reserve != nil || selection.Cold != nil || calls != 1 {
			t.Fatalf("country metadata %q changed pinned selection: %v", country, err)
		}
	}
	batch.Target = Target{Kind: TargetPrivatePeer, PeerID: other.PeerID}
	if _, err := EvaluateBatch(context.Background(), batch, probe, config); err == nil {
		t.Fatal("provider changed requested private identity")
	}
	for _, change := range []func(*Candidate){
		func(c *Candidate) { c.Capabilities |= protocol.CapExit },
		func(c *Candidate) { c.Transport = CandidateTransportDirect },
		func(c *Candidate) { c.ExpiresAt = now },
		func(c *Candidate) { c.DescriptorID = [32]byte{} },
	} {
		bad := wanted
		change(&bad)
		if err := validateCandidateTarget(bad, target, local, now); err == nil {
			t.Fatal("invalid private candidate accepted")
		}
	}
}

func TestTargetRejectsAmbiguityAndPreservesLegacyCountry(t *testing.T) {
	got, err := resolveTarget(Target{}, "nl")
	if err != nil || got.Kind != TargetPublicCountry || got.Country != "NL" {
		t.Fatal("legacy country resolution changed")
	}
	peer := selectorPeer(t, 5)
	for _, target := range []Target{
		{Kind: TargetPrivatePeer, Country: "NL", PeerID: peer},
		{Kind: TargetPublicCountry, Country: "NL", PeerID: peer},
		{Kind: TargetPrivatePeer},
		{Kind: 99, PeerID: peer},
		{Kind: TargetPrivatePeer, PeerID: "invalid"},
	} {
		if _, err := resolveTarget(target, ""); err == nil {
			t.Fatal("ambiguous target accepted")
		}
	}
	if _, err := resolveTarget(Target{Kind: TargetPrivatePeer, PeerID: peer}, "NL"); err == nil {
		t.Fatal("private target accepted legacy country")
	}
	s := &Selector{config: Config{Target: Target{Kind: TargetPrivatePeer, PeerID: peer}}}
	if err := s.SetCountry("NL"); err == nil {
		t.Fatal("country command changed pinned private target")
	}
}
