// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Command hydra-test-socketmux checks production mux invariants inside M4b.
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

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

type result struct {
	Type               string          `json:"type"`
	PID                int             `json:"pid"`
	Port               uint16          `json:"port"`
	LocalIPv4          string          `json:"local_ipv4,omitempty"`
	LocalIPv6          string          `json:"local_ipv6,omitempty"`
	ControlSourceIPv4  string          `json:"control_source_ipv4,omitempty"`
	DataSourceIPv4     string          `json:"data_source_ipv4,omitempty"`
	ControlSourceIPv6  string          `json:"control_source_ipv6,omitempty"`
	DataSourceIPv6     string          `json:"data_source_ipv6,omitempty"`
	Stats              socketmux.Stats `json:"stats"`
	BlockedReadersExit bool            `json:"blocked_readers_exit"`
	Error              string          `json:"error,omitempty"`
}

func main() {
	value, err := run()
	if err != nil {
		value.Error = err.Error()
	}
	_ = json.NewEncoder(os.Stdout).Encode(value)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (value result, runErr error) {
	bind4Text := flag.String("bind4", "", "concrete IPv4 bind address")
	bind6Text := flag.String("bind6", "", "concrete IPv6 bind address")
	port := flag.Int("port", 0, "shared UDP port")
	readyFile := flag.String("ready-file", "", "atomic readiness JSON path")
	startFile := flag.String("start-file", "", "file releasing the exercise")
	resultFile := flag.String("result-file", "", "atomic result JSON path")
	timeout := flag.Duration("timeout", 5*time.Second, "exercise timeout")
	flag.Parse()
	value = result{Type: "result", PID: os.Getpid()}
	if *port < 0 || *port > 65535 || *timeout <= 0 {
		return value, errors.New("invalid port or timeout")
	}
	bind4, err := parseAddress(*bind4Text, true)
	if err != nil {
		return value, err
	}
	bind6, err := parseAddress(*bind6Text, false)
	if err != nil {
		return value, err
	}
	if !bind4.IsValid() && !bind6.IsValid() {
		return value, errors.New("at least one bind address is required")
	}
	config := socketmux.DefaultConfig()
	config.Port = *port
	config.ControlQueueCapacity = 1
	config.DataQueueCapacity = 1
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	config.Classifier = socketmux.ClassifierFunc(func(payload []byte, _ netip.AddrPort, _ socketmux.AddressFamily) socketmux.PacketClass {
		if len(payload) > 0 && payload[0] == 1 {
			return socketmux.Control
		}
		return socketmux.Data
	})
	if bind4.IsValid() {
		config.IPv4Bind = bind4
		config.IPv4Policy = socketmux.FamilyRequired
	} else {
		config.IPv4Policy = socketmux.FamilyDisabled
	}
	if bind6.IsValid() {
		config.IPv6Bind = bind6
		config.IPv6Policy = socketmux.FamilyRequired
	} else {
		config.IPv6Policy = socketmux.FamilyDisabled
	}
	root, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	mux, err := socketmux.New(root, config)
	if err != nil {
		return value, err
	}
	closed := false
	defer func() {
		if !closed {
			runErr = errors.Join(runErr, mux.Close())
		}
	}()
	value.Port = mux.Port()
	if endpoint, ok := mux.LocalAddr(socketmux.IPv4); ok {
		value.LocalIPv4 = endpoint.String()
	}
	if endpoint, ok := mux.LocalAddr(socketmux.IPv6); ok {
		value.LocalIPv6 = endpoint.String()
	}
	if err := writeJSON(*readyFile, value); err != nil {
		return value, err
	}
	if err := waitForFile(root, *startFile); err != nil {
		return value, err
	}
	for _, family := range []socketmux.AddressFamily{socketmux.IPv4, socketmux.IPv6} {
		local, ok := mux.LocalAddr(family)
		if !ok {
			continue
		}
		controlSource, dataSource, err := exerciseFamily(root, mux, family, local)
		if err != nil {
			return value, err
		}
		if family == socketmux.IPv4 {
			value.ControlSourceIPv4, value.DataSourceIPv4 = controlSource.String(), dataSource.String()
		} else {
			value.ControlSourceIPv6, value.DataSourceIPv6 = controlSource.String(), dataSource.String()
		}
	}
	value.Stats = mux.Stats()
	readerErrors := make(chan error, 2)
	var readers sync.WaitGroup
	for _, endpoint := range []*socketmux.Endpoint{mux.Control(), mux.Data()} {
		readers.Add(1)
		go func(endpoint *socketmux.Endpoint) {
			defer readers.Done()
			_, err := endpoint.Read(context.Background())
			readerErrors <- err
		}(endpoint)
	}
	if err := mux.Close(); err != nil {
		return value, err
	}
	closed = true
	readers.Wait()
	close(readerErrors)
	for err := range readerErrors {
		if !errors.Is(err, socketmux.ErrClosed) {
			return value, fmt.Errorf("blocked reader close result: %w", err)
		}
	}
	if err := mux.Close(); err != nil {
		return value, err
	}
	value.BlockedReadersExit = true
	if *resultFile != "" {
		if err := writeJSON(*resultFile, value); err != nil {
			return value, err
		}
	}
	return value, nil
}

func exerciseFamily(ctx context.Context, mux *socketmux.Mux, family socketmux.AddressFamily, local netip.AddrPort) (netip.AddrPort, netip.AddrPort, error) {
	network := "udp6"
	if family == socketmux.IPv4 {
		network = "udp4"
	}
	peer, err := net.ListenUDP(network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(local.Addr(), 0)))
	if err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	defer peer.Close()
	write := func(payload []byte) error {
		_, err := peer.WriteToUDPAddrPort(payload, local)
		return err
	}
	controlDrops := mux.Stats().ControlOverflowDrops
	if err := write([]byte{1, 1}); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if err := write([]byte{1, 2}); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if err := waitForStat(ctx, func(stats socketmux.Stats) bool { return stats.ControlOverflowDrops > controlDrops }, mux); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if err := write([]byte{2, 1}); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if _, err := mux.Data().Read(ctx); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if _, err := mux.Control().Read(ctx); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	dataDrops := mux.Stats().DataOverflowDrops
	if err := write([]byte{2, 2}); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if err := write([]byte{2, 3}); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if err := waitForStat(ctx, func(stats socketmux.Stats) bool { return stats.DataOverflowDrops > dataDrops }, mux); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if err := write([]byte{1, 3}); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if _, err := mux.Control().Read(ctx); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if _, err := mux.Data().Read(ctx); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}

	peerEndpoint := peer.LocalAddr().(*net.UDPAddr).AddrPort()
	if _, err := mux.Control().WriteTo(ctx, []byte("control"), peerEndpoint); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	controlSource, err := readSource(ctx, peer)
	if err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if _, err := mux.Data().WriteTo(ctx, []byte("data"), peerEndpoint); err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	dataSource, err := readSource(ctx, peer)
	if err != nil {
		return netip.AddrPort{}, netip.AddrPort{}, err
	}
	if controlSource.Port() != mux.Port() || dataSource.Port() != mux.Port() {
		return netip.AddrPort{}, netip.AddrPort{}, errors.New("logical endpoint used a non-mux source port")
	}
	return controlSource, dataSource, nil
}

func readSource(ctx context.Context, conn *net.UDPConn) (netip.AddrPort, error) {
	deadline, ok := ctx.Deadline()
	if ok {
		_ = conn.SetReadDeadline(deadline)
	}
	buffer := make([]byte, 64)
	_, source, err := conn.ReadFromUDPAddrPort(buffer)
	return source, err
}

func waitForStat(ctx context.Context, predicate func(socketmux.Stats) bool, mux *socketmux.Mux) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if predicate(mux.Stats()) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func parseAddress(value string, ipv4 bool) (netip.Addr, error) {
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

func waitForFile(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
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
	if path == "" {
		return nil
	}
	temporary := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
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
