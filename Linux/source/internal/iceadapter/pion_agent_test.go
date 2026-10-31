// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package iceadapter

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/ice/v4"
)

func TestM5A_PionAgentHostCandidatesUseIrvioMux(t *testing.T) {
	stack := newTestStack(t, true, 32)
	before4, err := processUDPSocketInodes("/proc/self/net/udp")
	if err != nil {
		t.Fatal(err)
	}
	before6, err := processUDPSocketInodes("/proc/self/net/udp6")
	if err != nil {
		t.Fatal(err)
	}
	if countUDPPortRows(before4, stack.mux.Port()) != 1 || countUDPPortRows(before6, stack.mux.Port()) != 1 {
		t.Fatalf("Irvio socket rows before Pion Agent = UDP4:%v UDP6:%v", before4, before6)
	}

	agent, err := ice.NewAgentWithOptions(
		ice.WithUDPMux(stack.adapter),
		ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost}),
		ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}),
		ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ufrag, password, err := agent.GetLocalUserCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if !validUfrag(ufrag) || password == "" {
		t.Fatalf("invalid local credentials: ufrag=%q password-empty=%v", ufrag, password == "")
	}

	var mu sync.Mutex
	var candidates []ice.Candidate
	done := make(chan struct{})
	var doneOnce sync.Once
	if err := agent.OnCandidate(func(candidate ice.Candidate) {
		if candidate == nil {
			doneOnce.Do(func() { close(done) })
			return
		}
		mu.Lock()
		candidates = append(candidates, candidate)
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	if err := agent.GatherCandidates(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("candidate gathering did not complete")
	}
	mu.Lock()
	gathered := append([]ice.Candidate(nil), candidates...)
	mu.Unlock()
	if len(gathered) != 2 {
		t.Fatalf("gathered %d candidates, want one UDP4 and one UDP6", len(gathered))
	}
	wantAddresses := map[string]ice.NetworkType{
		"127.0.0.1": ice.NetworkTypeUDP4,
		"::1":       ice.NetworkTypeUDP6,
	}
	for _, candidate := range gathered {
		if candidate.Type() != ice.CandidateTypeHost {
			t.Fatalf("candidate type = %v", candidate.Type())
		}
		if candidate.Port() != int(stack.mux.Port()) {
			t.Fatalf("candidate port = %d, mux port = %d", candidate.Port(), stack.mux.Port())
		}
		wantNetwork, exists := wantAddresses[candidate.Address()]
		if !exists || candidate.NetworkType() != wantNetwork {
			t.Fatalf("unexpected candidate %s/%s", candidate.NetworkType(), candidate.Address())
		}
		delete(wantAddresses, candidate.Address())
	}
	if len(wantAddresses) != 0 {
		t.Fatalf("missing candidate addresses: %v", wantAddresses)
	}
	if stats := stack.adapter.Stats(); stats.ActiveUfrags != 1 {
		t.Fatalf("active ufrags after gathering = %d", stats.ActiveUfrags)
	}

	after4, err := processUDPSocketInodes("/proc/self/net/udp")
	if err != nil {
		t.Fatal(err)
	}
	after6, err := processUDPSocketInodes("/proc/self/net/udp6")
	if err != nil {
		t.Fatal(err)
	}
	if !sameUDPSocketRows(after4, before4) || !sameUDPSocketRows(after6, before6) {
		t.Fatalf("Pion changed process UDP sockets: before=%v/%v after=%v/%v", before4, before6, after4, after6)
	}
	if err := agent.GracefulClose(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return stack.adapter.Stats().ActiveUfrags == 0 })
}

func processUDPSocketInodes(path string) (map[uint64]uint16, error) {
	owned, err := processSocketInodes()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows := make(map[uint64]uint16)
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return nil, scanner.Err()
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		parts := strings.Split(fields[1], ":")
		if len(parts) != 2 {
			continue
		}
		port, err := strconv.ParseUint(parts[1], 16, 16)
		if err != nil {
			return nil, err
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, err
		}
		if _, exists := owned[inode]; exists {
			rows[inode] = uint16(port)
		}
	}
	return rows, scanner.Err()
}

func processSocketInodes() (map[uint64]struct{}, error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return nil, err
	}
	inodes := make(map[uint64]struct{})
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil {
			continue
		}
		if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
			continue
		}
		inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse socket inode %q: %w", target, err)
		}
		inodes[inode] = struct{}{}
	}
	return inodes, nil
}

func countUDPPortRows(rows map[uint64]uint16, port uint16) int {
	count := 0
	for _, rowPort := range rows {
		if rowPort == port {
			count++
		}
	}
	return count
}

func sameUDPSocketRows(a, b map[uint64]uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for inode, port := range a {
		if b[inode] != port {
			return false
		}
	}
	return true
}
