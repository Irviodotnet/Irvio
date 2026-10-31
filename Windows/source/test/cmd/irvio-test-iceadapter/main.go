// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Command hydra-test-iceadapter exercises M5a inside privileged network namespaces.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

type candidate struct {
	Type    string `json:"type"`
	Network string `json:"network"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type report struct {
	Type         string           `json:"type"`
	PID          int              `json:"pid"`
	Port         uint16           `json:"port"`
	LocalIPv4    string           `json:"local_ipv4,omitempty"`
	LocalIPv6    string           `json:"local_ipv6,omitempty"`
	LocalUfrag   string           `json:"local_ufrag,omitempty"`
	Candidates   []candidate      `json:"candidates,omitempty"`
	OutboundIPv4 bool             `json:"outbound_ipv4"`
	OutboundIPv6 bool             `json:"outbound_ipv6"`
	DataAfterICE bool             `json:"data_after_ice"`
	AdapterStats iceadapter.Stats `json:"adapter_stats"`
	Error        string           `json:"error,omitempty"`
}

type options struct {
	bind4, bind6           netip.Addr
	receiver4, receiver6   netip.AddrPort
	port                   int
	ready, start, gathered string
	finish, result         string
	timeout                time.Duration
}

func main() {
	value, resultPath, err := run()
	if err != nil {
		value.Error = err.Error()
	}
	if resultPath != "" {
		if writeErr := writeJSON(resultPath, value); writeErr != nil && err == nil {
			err = writeErr
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(value)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (value report, resultPath string, runErr error) {
	value = report{Type: "result", PID: os.Getpid()}
	parsed, err := parseOptions()
	if err != nil {
		return value, "", err
	}
	resultPath = parsed.result
	root, cancel := context.WithTimeout(context.Background(), parsed.timeout)
	defer cancel()
	registry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		return value, resultPath, err
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = parsed.port
	muxConfig.Classifier = iceadapter.NewClassifier(registry)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	addresses := make([]netip.Addr, 0, 2)
	if parsed.bind4.IsValid() {
		muxConfig.IPv4Policy = socketmux.FamilyRequired
		muxConfig.IPv4Bind = parsed.bind4
		addresses = append(addresses, parsed.bind4)
	} else {
		muxConfig.IPv4Policy = socketmux.FamilyDisabled
	}
	if parsed.bind6.IsValid() {
		muxConfig.IPv6Policy = socketmux.FamilyRequired
		muxConfig.IPv6Bind = parsed.bind6
		addresses = append(addresses, parsed.bind6)
	} else {
		muxConfig.IPv6Policy = socketmux.FamilyDisabled
	}
	mux, err := socketmux.New(root, muxConfig)
	if err != nil {
		return value, resultPath, err
	}
	defer func() { runErr = errors.Join(runErr, mux.Close()) }()
	adapterConfig := iceadapter.DefaultConfig(addresses...)
	adapterConfig.Logger = muxConfig.Logger
	adapter, err := iceadapter.New(mux.Control(), registry, adapterConfig)
	if err != nil {
		return value, resultPath, err
	}
	adapterClosed := false
	defer func() {
		if !adapterClosed {
			runErr = errors.Join(runErr, adapter.Close())
		}
	}()
	value.Port = mux.Port()
	if local, ok := mux.LocalAddr(socketmux.IPv4); ok {
		value.LocalIPv4 = local.String()
	}
	if local, ok := mux.LocalAddr(socketmux.IPv6); ok {
		value.LocalIPv6 = local.String()
	}
	if err := writeJSON(parsed.ready, value); err != nil {
		return value, resultPath, err
	}
	if err := waitForFile(root, parsed.start); err != nil {
		return value, resultPath, err
	}

	agent, err := ice.NewAgentWithOptions(
		ice.WithUDPMux(adapter),
		ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost}),
		ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}),
		ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled),
	)
	if err != nil {
		return value, resultPath, err
	}
	agentClosed := false
	defer func() {
		if !agentClosed {
			runErr = errors.Join(runErr, agent.Close())
		}
	}()
	ufrag, password, err := agent.GetLocalUserCredentials()
	if err != nil {
		return value, resultPath, fmt.Errorf("get Pion local credentials: %w", err)
	}
	if password == "" {
		return value, resultPath, errors.New("Pion returned an empty local password")
	}
	value.LocalUfrag = ufrag
	var candidatesMu sync.Mutex
	gatherDone := make(chan struct{})
	var gatherOnce sync.Once
	if err := agent.OnCandidate(func(found ice.Candidate) {
		if found == nil {
			gatherOnce.Do(func() { close(gatherDone) })
			return
		}
		candidatesMu.Lock()
		value.Candidates = append(value.Candidates, candidate{
			Type: found.Type().String(), Network: found.NetworkType().String(),
			Address: found.Address(), Port: found.Port(),
		})
		candidatesMu.Unlock()
	}); err != nil {
		return value, resultPath, err
	}
	if err := agent.GatherCandidates(); err != nil {
		return value, resultPath, err
	}
	select {
	case <-root.Done():
		return value, resultPath, root.Err()
	case <-gatherDone:
	}
	if parsed.receiver4.IsValid() {
		value.OutboundIPv4, err = sendBinding(adapter, parsed.bind4, parsed.receiver4, value.Port, "manual4")
		if err != nil {
			return value, resultPath, err
		}
	}
	if parsed.receiver6.IsValid() {
		value.OutboundIPv6, err = sendBinding(adapter, parsed.bind6, parsed.receiver6, value.Port, "manual6")
		if err != nil {
			return value, resultPath, err
		}
	}
	value.AdapterStats = adapter.Stats()
	if err := writeJSON(parsed.gathered, value); err != nil {
		return value, resultPath, err
	}
	if err := waitForFile(root, parsed.finish); err != nil {
		return value, resultPath, err
	}
	if err := agent.GracefulClose(); err != nil {
		return value, resultPath, err
	}
	agentClosed = true
	if err := adapter.Close(); err != nil {
		return value, resultPath, err
	}
	adapterClosed = true
	dataDestination := parsed.receiver4
	if !dataDestination.IsValid() {
		dataDestination = parsed.receiver6
	}
	if dataDestination.IsValid() {
		if _, err := mux.Data().WriteTo(root, []byte("m5a-data-after-ice"), dataDestination); err != nil {
			return value, resultPath, err
		}
		value.DataAfterICE = true
	}
	value.AdapterStats = adapter.Stats()
	return value, resultPath, nil
}

func sendBinding(adapter *iceadapter.Adapter, local netip.Addr, remote netip.AddrPort, port uint16, ufrag string) (bool, error) {
	connection, err := adapter.GetConn(ufrag, net.UDPAddrFromAddrPort(netip.AddrPortFrom(local, port)))
	if err != nil {
		return false, err
	}
	defer connection.Close()
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest, stun.NewUsername("remote:"+ufrag))
	if _, err := connection.WriteTo(request.Raw, net.UDPAddrFromAddrPort(remote)); err != nil {
		return false, err
	}
	return true, nil
}

func parseOptions() (options, error) {
	bind4Text := flag.String("bind4", "", "concrete IPv4 bind address")
	bind6Text := flag.String("bind6", "", "concrete IPv6 bind address")
	receiver4Text := flag.String("receiver4", "", "IPv4 ICE request destination")
	receiver6Text := flag.String("receiver6", "", "IPv6 ICE request destination")
	port := flag.Int("port", 0, "shared Irvio UDP port")
	ready := flag.String("ready-file", "", "pre-Agent readiness JSON path")
	start := flag.String("start-file", "", "file releasing Agent construction")
	gathered := flag.String("gathered-file", "", "post-gathering JSON path")
	finish := flag.String("finish-file", "", "file releasing shutdown")
	result := flag.String("result-file", "", "final JSON path")
	timeout := flag.Duration("timeout", 10*time.Second, "bounded helper timeout")
	flag.Parse()
	if *port < 0 || *port > 65535 || *timeout <= 0 || *ready == "" || *start == "" || *gathered == "" || *finish == "" || *result == "" {
		return options{}, errors.New("invalid port, timeout, or synchronization path")
	}
	parseAddr := func(text string) (netip.Addr, error) {
		if text == "" {
			return netip.Addr{}, nil
		}
		return netip.ParseAddr(text)
	}
	parseEndpoint := func(text string) (netip.AddrPort, error) {
		if text == "" {
			return netip.AddrPort{}, nil
		}
		return netip.ParseAddrPort(text)
	}
	bind4, err := parseAddr(*bind4Text)
	if err != nil {
		return options{}, err
	}
	bind6, err := parseAddr(*bind6Text)
	if err != nil {
		return options{}, err
	}
	receiver4, err := parseEndpoint(*receiver4Text)
	if err != nil {
		return options{}, err
	}
	receiver6, err := parseEndpoint(*receiver6Text)
	if err != nil {
		return options{}, err
	}
	if !bind4.IsValid() && !bind6.IsValid() {
		return options{}, errors.New("at least one bind address is required")
	}
	return options{
		bind4: bind4, bind6: bind6, receiver4: receiver4, receiver6: receiver6,
		port: *port, ready: *ready, start: *start, gathered: *gathered,
		finish: *finish, result: *result, timeout: *timeout,
	}, nil
}

func waitForFile(ctx context.Context, path string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func writeJSON(path string, value any) error {
	temporary := path + fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return errors.Join(encodeErr, closeErr)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
