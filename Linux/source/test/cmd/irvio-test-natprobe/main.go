// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Command hydra-test-natprobe exercises production mux, STUN, and NAT profiling inside M4b.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Irviodotnet/Irvio/internal/natprobe"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

type report struct {
	Type        string            `json:"type"`
	Mode        string            `json:"mode"`
	PID         int               `json:"pid"`
	Port        uint16            `json:"port"`
	LocalIPv4   string            `json:"local_ipv4,omitempty"`
	LocalIPv6   string            `json:"local_ipv6,omitempty"`
	BasicIPv4   *stunprobe.Result `json:"basic_ipv4,omitempty"`
	BasicIPv6   *stunprobe.Result `json:"basic_ipv6,omitempty"`
	ProfileIPv4 *natprobe.Profile `json:"profile_ipv4,omitempty"`
	ProfileIPv6 *natprobe.Profile `json:"profile_ipv6,omitempty"`
	MuxStats    socketmux.Stats   `json:"mux_stats"`
	STUNStats   stunprobe.Stats   `json:"stun_stats"`
	NATStats    natprobe.Stats    `json:"nat_stats"`
	Error       string            `json:"error,omitempty"`
}

type options struct {
	mode       string
	bind4      netip.Addr
	bind6      netip.Addr
	port       int
	server4    netip.AddrPort
	server6    netip.AddrPort
	readyFile  string
	startFile  string
	resultFile string
	timeout    time.Duration
	initialRTO time.Duration
	maxRTO     time.Duration
	attempts   int
	pacing     time.Duration
}

func main() {
	result, err := run()
	if err != nil {
		result.Error = err.Error()
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil && err == nil {
		err = encodeErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (result report, runErr error) {
	opts, err := parseOptions()
	if err != nil {
		return report{Type: "result"}, err
	}
	result = report{Type: "result", Mode: opts.mode, PID: os.Getpid()}

	registry, err := stunprobe.NewRegistry(stunprobe.DefaultRegistryConfig())
	if err != nil {
		return result, err
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = opts.port
	muxConfig.Classifier = stunprobe.NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if opts.bind4.IsValid() {
		muxConfig.IPv4Policy = socketmux.FamilyRequired
		muxConfig.IPv4Bind = opts.bind4
	} else {
		muxConfig.IPv4Policy = socketmux.FamilyDisabled
	}
	if opts.bind6.IsValid() {
		muxConfig.IPv6Policy = socketmux.FamilyRequired
		muxConfig.IPv6Bind = opts.bind6
	} else {
		muxConfig.IPv6Policy = socketmux.FamilyDisabled
	}

	root, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	mux, err := socketmux.New(root, muxConfig)
	if err != nil {
		return result, err
	}
	defer func() {
		if err := mux.Close(); err != nil && runErr == nil {
			runErr = err
		}
	}()
	result.Port = mux.Port()
	if endpoint, ok := mux.LocalAddr(socketmux.IPv4); ok {
		result.LocalIPv4 = endpoint.String()
	}
	if endpoint, ok := mux.LocalAddr(socketmux.IPv6); ok {
		result.LocalIPv6 = endpoint.String()
	}
	if err := writeReady(opts.readyFile, result); err != nil {
		return result, err
	}
	if opts.mode == "socket-info" {
		<-root.Done()
		result.MuxStats = mux.Stats()
		return result, nil
	}
	if err := waitForStart(root, opts.startFile, opts.timeout); err != nil {
		return result, err
	}

	client, err := stunprobe.NewClient(mux.Control(), registry, stunprobe.Config{
		InitialRTO: opts.initialRTO, MaxRTO: opts.maxRTO, MaxAttempts: opts.attempts,
	})
	if err != nil {
		return result, err
	}
	defer func() {
		if err := client.Close(); err != nil && runErr == nil {
			runErr = err
		}
	}()
	profiler, err := natprobe.New(client, natprobe.Config{
		MinimumTransactionInterval: opts.pacing, MaxObservations: 16,
	})
	if err != nil {
		return result, err
	}
	defer func() {
		if err := profiler.Close(); err != nil && runErr == nil {
			runErr = err
		}
	}()

	operationContext, cancel := context.WithTimeout(root, opts.timeout)
	defer cancel()
	err = execute(operationContext, opts, &result, client, profiler)
	result.MuxStats = mux.Stats()
	result.STUNStats = client.Stats()
	result.NATStats = profiler.Stats()
	if err != nil {
		result.Error = err.Error()
	}
	if opts.resultFile != "" {
		if writeErr := writeJSON(opts.resultFile, result); writeErr != nil && err == nil {
			err = writeErr
		}
	}
	return result, err
}

func parseOptions() (options, error) {
	mode := flag.String("mode", "profile", "basic, basic-dual, profile, profile-dual, or socket-info")
	bind4Text := flag.String("bind4", "", "concrete IPv4 bind address; empty disables IPv4")
	bind6Text := flag.String("bind6", "", "concrete IPv6 bind address; empty disables IPv6")
	port := flag.Int("port", 0, "shared mux UDP port")
	server4Text := flag.String("server4", "", "IPv4 STUN server endpoint")
	server6Text := flag.String("server6", "", "IPv6 STUN server endpoint")
	readyFile := flag.String("ready-file", "", "atomic JSON readiness file")
	startFile := flag.String("start-file", "", "file that releases the operation")
	resultFile := flag.String("result-file", "", "atomic JSON result file")
	timeout := flag.Duration("timeout", 8*time.Second, "overall operation timeout")
	initialRTO := flag.Duration("initial-rto", 100*time.Millisecond, "STUN initial retransmission timeout")
	maxRTO := flag.Duration("max-rto", 400*time.Millisecond, "STUN maximum retransmission timeout")
	attempts := flag.Int("attempts", 3, "STUN maximum attempts")
	pacing := flag.Duration("pacing", 100*time.Millisecond, "minimum interval between profile transactions")
	flag.Parse()

	validModes := map[string]bool{"basic": true, "basic-dual": true, "profile": true, "profile-dual": true, "socket-info": true}
	if !validModes[*mode] {
		return options{}, fmt.Errorf("invalid --mode %q", *mode)
	}
	if *port < 0 || *port > 65535 {
		return options{}, errors.New("--port must be within 0..65535")
	}
	if *timeout <= 0 {
		return options{}, errors.New("--timeout must be positive")
	}
	bind4, err := parseOptionalAddr(*bind4Text, true)
	if err != nil {
		return options{}, fmt.Errorf("parse --bind4: %w", err)
	}
	bind6, err := parseOptionalAddr(*bind6Text, false)
	if err != nil {
		return options{}, fmt.Errorf("parse --bind6: %w", err)
	}
	if !bind4.IsValid() && !bind6.IsValid() {
		return options{}, errors.New("at least one bind address is required")
	}
	server4, err := parseOptionalAddrPort(*server4Text, true)
	if err != nil {
		return options{}, fmt.Errorf("parse --server4: %w", err)
	}
	server6, err := parseOptionalAddrPort(*server6Text, false)
	if err != nil {
		return options{}, fmt.Errorf("parse --server6: %w", err)
	}
	return options{
		mode: *mode, bind4: bind4, bind6: bind6, port: *port,
		server4: server4, server6: server6, readyFile: *readyFile,
		startFile: *startFile, resultFile: *resultFile, timeout: *timeout,
		initialRTO: *initialRTO, maxRTO: *maxRTO, attempts: *attempts, pacing: *pacing,
	}, nil
}

func parseOptionalAddr(value string, ipv4 bool) (netip.Addr, error) {
	if value == "" {
		return netip.Addr{}, nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, err
	}
	if address.Is4In6() || address.Is4() != ipv4 {
		return netip.Addr{}, errors.New("address family mismatch")
	}
	return address, nil
}

func parseOptionalAddrPort(value string, ipv4 bool) (netip.AddrPort, error) {
	if value == "" {
		return netip.AddrPort{}, nil
	}
	endpoint, err := netip.ParseAddrPort(value)
	if err != nil {
		return netip.AddrPort{}, err
	}
	if endpoint.Port() == 0 || endpoint.Addr().Is4In6() || endpoint.Addr().Is4() != ipv4 {
		return netip.AddrPort{}, errors.New("endpoint family mismatch or zero port")
	}
	return endpoint, nil
}

func execute(ctx context.Context, opts options, result *report, client *stunprobe.Client, profiler *natprobe.Profiler) error {
	switch opts.mode {
	case "basic":
		server := opts.server4
		if !server.IsValid() {
			server = opts.server6
		}
		if !server.IsValid() {
			return errors.New("basic mode requires one server")
		}
		value, err := client.Probe(ctx, server)
		if server.Addr().Is4() {
			result.BasicIPv4 = &value
		} else {
			result.BasicIPv6 = &value
		}
		return err
	case "profile":
		server := opts.server4
		if !server.IsValid() {
			server = opts.server6
		}
		if !server.IsValid() {
			return errors.New("profile mode requires one server")
		}
		value, err := profiler.Profile(ctx, server)
		if server.Addr().Is4() {
			result.ProfileIPv4 = &value
		} else {
			result.ProfileIPv6 = &value
		}
		return err
	case "basic-dual":
		return runDual(ctx, opts.server4, opts.server6,
			func(ctx context.Context, endpoint netip.AddrPort) error {
				value, err := client.Probe(ctx, endpoint)
				if endpoint.Addr().Is4() {
					result.BasicIPv4 = &value
				} else {
					result.BasicIPv6 = &value
				}
				return err
			})
	case "profile-dual":
		return runDual(ctx, opts.server4, opts.server6,
			func(ctx context.Context, endpoint netip.AddrPort) error {
				value, err := profiler.Profile(ctx, endpoint)
				if endpoint.Addr().Is4() {
					result.ProfileIPv4 = &value
				} else {
					result.ProfileIPv6 = &value
				}
				return err
			})
	default:
		return fmt.Errorf("unsupported operation mode %q", opts.mode)
	}
}

func runDual(ctx context.Context, ipv4, ipv6 netip.AddrPort, operation func(context.Context, netip.AddrPort) error) error {
	if !ipv4.IsValid() || !ipv6.IsValid() {
		return errors.New("dual mode requires both server endpoints")
	}
	var wg sync.WaitGroup
	errorsByFamily := make(chan error, 2)
	for _, endpoint := range []netip.AddrPort{ipv4, ipv6} {
		wg.Add(1)
		go func(server netip.AddrPort) {
			defer wg.Done()
			if err := operation(ctx, server); err != nil {
				errorsByFamily <- fmt.Errorf("%s operation: %w", familyName(server), err)
			}
		}(endpoint)
	}
	wg.Wait()
	close(errorsByFamily)
	var collected []error
	for err := range errorsByFamily {
		collected = append(collected, err)
	}
	return errors.Join(collected...)
}

func familyName(endpoint netip.AddrPort) string {
	if endpoint.Addr().Is4() {
		return "IPv4"
	}
	return "IPv6"
}

func writeReady(path string, value report) error {
	if path == "" {
		return nil
	}
	ready := struct {
		Type      string `json:"type"`
		PID       int    `json:"pid"`
		Port      uint16 `json:"port"`
		LocalIPv4 string `json:"local_ipv4,omitempty"`
		LocalIPv6 string `json:"local_ipv6,omitempty"`
	}{Type: "ready", PID: value.PID, Port: value.Port, LocalIPv4: value.LocalIPv4, LocalIPv6: value.LocalIPv6}
	return writeJSON(path, ready)
}

func waitForStart(ctx context.Context, path string, maximum time.Duration) error {
	if path == "" {
		return nil
	}
	waitContext, cancel := context.WithTimeout(ctx, maximum)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check start file: %w", err)
		}
		select {
		case <-waitContext.Done():
			return waitContext.Err()
		case <-ticker.C:
		}
	}
}

func writeJSON(path string, value any) error {
	temporary := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create JSON temporary file: %w", err)
	}
	encodeErr := json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if encodeErr != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("encode JSON: %w", encodeErr)
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("close JSON: %w", closeErr)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish JSON: %w", err)
	}
	return nil
}
