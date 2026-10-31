// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/natprobe"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
	"github.com/Irviodotnet/Irvio/test/support/rfc5780"
)

type probeReport struct {
	Type        string            `json:"type"`
	Mode        string            `json:"mode"`
	PID         int               `json:"pid"`
	Port        uint16            `json:"port"`
	LocalIPv4   string            `json:"local_ipv4"`
	LocalIPv6   string            `json:"local_ipv6"`
	BasicIPv4   *stunprobe.Result `json:"basic_ipv4"`
	BasicIPv6   *stunprobe.Result `json:"basic_ipv6"`
	ProfileIPv4 *natprobe.Profile `json:"profile_ipv4"`
	ProfileIPv6 *natprobe.Profile `json:"profile_ipv6"`
	MuxStats    socketmux.Stats   `json:"mux_stats"`
	STUNStats   stunprobe.Stats   `json:"stun_stats"`
	NATStats    natprobe.Stats    `json:"nat_stats"`
	Error       string            `json:"error"`
}

type probeReady struct {
	Type      string `json:"type"`
	PID       int    `json:"pid"`
	Port      uint16 `json:"port"`
	LocalIPv4 string `json:"local_ipv4"`
	LocalIPv6 string `json:"local_ipv6"`
}

type responderProcess struct {
	child            *childProcess
	observationPath  string
	readyPath        string
	baselineReady    string
	baselineContinue string
}

func (l *lab) startResponder(label, namespace string, a1, a2 netip.Addr, extraArgs ...string) responderProcess {
	l.t.Helper()
	ready := filepath.Join(l.artifactDir, label+"-ready.json")
	observations := filepath.Join(l.artifactDir, label+"-observations.jsonl")
	snapshot := filepath.Join(l.artifactDir, label+"-observations.json")
	args := []string{
		"--a1", a1.String(), "--a2", a2.String(),
		"--p1", strconv.Itoa(stunPrimaryPort), "--p2", strconv.Itoa(stunAlternatePort),
		"--ready-file", ready, "--observations", observations, "--snapshot", snapshot,
	}
	args = append(args, extraArgs...)
	child := l.startChild(label, namespace, os.Getenv("HYDRA_M4B_RFC5780"), args...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, ready); err != nil {
		l.t.Fatalf("wait for %s readiness: %v", label, err)
	}
	return responderProcess{child: child, observationPath: observations, readyPath: ready}
}

type probeOptions struct {
	Mode       string
	BindIPv4   netip.Addr
	BindIPv6   netip.Addr
	Port       uint16
	ServerIPv4 netip.AddrPort
	ServerIPv6 netip.AddrPort
	Timeout    time.Duration
	InitialRTO time.Duration
	MaxRTO     time.Duration
	Attempts   int
	Pacing     time.Duration
}

func (l *lab) runProbe(label, namespace string, options probeOptions, ready func(probeReady)) (probeReport, error) {
	l.t.Helper()
	readyPath := filepath.Join(l.artifactDir, label+"-ready.json")
	startPath := filepath.Join(l.artifactDir, label+"-start")
	resultPath := filepath.Join(l.artifactDir, label+"-result.json")
	if options.Timeout == 0 {
		options.Timeout = 8 * time.Second
	}
	if options.InitialRTO == 0 {
		options.InitialRTO = 100 * time.Millisecond
	}
	if options.MaxRTO == 0 {
		options.MaxRTO = 400 * time.Millisecond
	}
	if options.Attempts == 0 {
		options.Attempts = 3
	}
	if options.Pacing == 0 {
		options.Pacing = 100 * time.Millisecond
	}
	args := []string{
		"--mode", options.Mode, "--port", strconv.Itoa(int(options.Port)),
		"--ready-file", readyPath, "--start-file", startPath, "--result-file", resultPath,
		"--timeout", options.Timeout.String(), "--initial-rto", options.InitialRTO.String(),
		"--max-rto", options.MaxRTO.String(), "--attempts", strconv.Itoa(options.Attempts),
		"--pacing", options.Pacing.String(),
	}
	if options.BindIPv4.IsValid() {
		args = append(args, "--bind4", options.BindIPv4.String())
	}
	if options.BindIPv6.IsValid() {
		args = append(args, "--bind6", options.BindIPv6.String())
	}
	if options.ServerIPv4.IsValid() {
		args = append(args, "--server4", options.ServerIPv4.String())
	}
	if options.ServerIPv6.IsValid() {
		args = append(args, "--server6", options.ServerIPv6.String())
	}
	child := l.startChild(label, namespace, os.Getenv("HYDRA_M4B_NATPROBE"), args...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForChildFile(ctx, child, readyPath); err != nil {
		return probeReport{}, fmt.Errorf("wait for client readiness: %w", err)
	}
	var readyValue probeReady
	if err := readJSON(readyPath, &readyValue); err != nil {
		return probeReport{}, fmt.Errorf("read client readiness: %w", err)
	}
	if ready != nil {
		ready(readyValue)
	}
	if err := touch(startPath); err != nil {
		return probeReport{}, fmt.Errorf("release client operation: %w", err)
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), options.Timeout+3*time.Second)
	defer waitCancel()
	waitErr := child.wait(waitContext)
	var result probeReport
	if err := readJSON(resultPath, &result); err != nil {
		if waitErr != nil {
			return result, fmt.Errorf("client exit: %w; result: %v", waitErr, err)
		}
		return result, err
	}
	if waitErr != nil {
		return result, waitErr
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	return result, nil
}

func waitForChildFile(ctx context.Context, child *childProcess, path string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-child.done:
			child.mu.Lock()
			err := child.waitErr
			child.mu.Unlock()
			return fmt.Errorf("child exited before readiness: %w", err)
		case <-ticker.C:
		}
	}
}

func readObservations(path string) ([]rfc5780.Observation, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var observations []rfc5780.Observation
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 4096)
	scanner.Buffer(buffer, 1<<20)
	for scanner.Scan() {
		var observation rfc5780.Observation
		if err := json.Unmarshal(scanner.Bytes(), &observation); err != nil {
			return nil, err
		}
		observations = append(observations, observation)
		if len(observations) > 65536 {
			return nil, errors.New("observation artifact exceeds bound")
		}
	}
	return observations, scanner.Err()
}

func assertProfile(t *testing.T, profile *natprobe.Profile, mapping natprobe.MappingBehavior, filtering natprobe.FilteringBehavior, translation natprobe.TranslationState) {
	t.Helper()
	if profile == nil {
		t.Fatal("missing profile")
	}
	if profile.Status != natprobe.ProfileComplete || profile.Mapping != mapping || profile.Filtering != filtering || profile.Translation != translation {
		t.Fatalf("unexpected profile: %+v", *profile)
	}
	if len(profile.Observations) == 0 || len(profile.Observations) > 16 {
		t.Fatalf("profile observations are not bounded: %d", len(profile.Observations))
	}
}

func assertServerOracle(t *testing.T, profile *natprobe.Profile, observations []rfc5780.Observation, internalPort uint16, externalPort uint16) {
	t.Helper()
	if len(observations) == 0 {
		t.Fatal("server recorded no Binding request")
	}
	baseline := observations[0]
	if profile.InitialMappedEndpoint != baseline.Source {
		t.Fatalf("profile mapped endpoint %s differs from server observation %s", profile.InitialMappedEndpoint, baseline.Source)
	}
	if externalPort != 0 && baseline.Source.Port() != externalPort {
		t.Fatalf("server source port = %d, want translated %d", baseline.Source.Port(), externalPort)
	}
	if profile.LocalEndpoint.Port() != internalPort {
		t.Fatalf("profile local port = %d, want mux port %d", profile.LocalEndpoint.Port(), internalPort)
	}
	seenChangedSource := false
	seenAlternateMapping := false
	for _, observation := range observations {
		if observation.ChangeRequest != rfc5780.ChangeNone {
			seenChangedSource = true
		}
		if observation.Destination != profile.PrimaryServer {
			if !seenChangedSource {
				t.Fatal("mapping traffic reached an alternate destination before filtering")
			}
			seenAlternateMapping = true
		}
	}
	if profile.Translation != natprobe.TranslationAbsent && profile.Mapping != natprobe.MappingUnknown && !seenAlternateMapping {
		t.Fatal("translated mapping classification lacked alternate-destination evidence")
	}
}

func requireCounter(t *testing.T, raw, comment string, minimum uint64) uint64 {
	t.Helper()
	count, ok := counterForComment(raw, comment)
	if !ok {
		t.Fatalf("nftables counter %q is missing", comment)
	}
	if count < minimum {
		t.Fatalf("nftables counter %q = %d, want at least %d", comment, count, minimum)
	}
	return count
}
