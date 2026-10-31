// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"
)

type attempt struct {
	Sequence   uint64 `json:"sequence"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	Lane       int    `json:"lane"`
	Phase      string `json:"phase"`
	WriteError bool   `json:"write_error"`
}

func run(args []string) error {
	if len(args) != 4 || !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(args[1]) {
		return fmt.Errorf("usage: leakprobe.exe phase-file run-id source-1 source-2")
	}
	var sockets []*net.UDPConn
	defer func() {
		for _, c := range sockets {
			c.Close()
		}
	}()
	for _, text := range args[2:] {
		a, err := netip.ParseAddr(text)
		if err != nil || !a.Is4() || !a.IsGlobalUnicast() {
			return fmt.Errorf("invalid source IPv4")
		}
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IP(a.AsSlice())})
		if err != nil {
			return err
		}
		sockets = append(sockets, c)
	}
	if args[2] == args[3] {
		return fmt.Errorf("distinct sources required")
	}
	destination := netip.MustParseAddrPort("198.51.100.77:49199")
	start := time.Now()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	phase := ""
	lastRead := time.Now()
	var sequence uint64
	encoder := json.NewEncoder(os.Stdout)
	for range ticker.C {
		if time.Since(start) > 10*time.Minute {
			return fmt.Errorf("probe lifetime exceeded")
		}
		data, err := os.ReadFile(args[0])
		if err == nil && strings.TrimSpace(string(data)) != "" {
			phase = strings.TrimSpace(string(data))
			lastRead = time.Now()
		} else if time.Since(lastRead) > time.Second {
			return fmt.Errorf("phase file unavailable")
		}
		if phase == "stop" {
			return nil
		}
		if phase != "baseline" && phase != "protected" && phase != "teardown" && phase != "after" {
			return fmt.Errorf("invalid phase")
		}
		for lane, c := range sockets {
			sequence++
			row := attempt{Sequence: sequence, ElapsedMS: time.Since(start).Milliseconds(), Lane: lane, Phase: phase}
			payload := []byte(fmt.Sprintf("irvio-w08-leak-v1|%s|%d|%s|%d", args[1], lane, phase, sequence))
			if err := c.SetWriteDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
				return err
			}
			n, err := c.WriteToUDPAddrPort(payload, destination)
			row.WriteError = err != nil || n != len(payload)
			if err := encoder.Encode(row); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	var err error
	if len(os.Args) > 1 && os.Args[1] == "check" {
		err = check(os.Args[2:])
	} else {
		err = run(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
