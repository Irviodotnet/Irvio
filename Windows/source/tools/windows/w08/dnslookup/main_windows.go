// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const queryOptions = 0x8 | 0x40 | 0x80 | 0x800 | 0x1000

type result struct {
	Addresses   []string `json:"addresses"`
	Error       string   `json:"error,omitempty"`
	DurationMS  int64    `json:"duration_ms"`
	BypassCache bool     `json:"bypass_cache"`
}

func addresses(head *windows.DNSRecord) ([]string, error) {
	var out []string
	seen := make(map[*windows.DNSRecord]bool)
	for r := head; r != nil; r = r.Next {
		if seen[r] || len(seen) >= 512 {
			return nil, fmt.Errorf("invalid DNS record chain")
		}
		seen[r] = true
		if r.Type != windows.DNS_TYPE_A || r.Dw&3 != 1 {
			continue
		}
		if r.Length != 4 {
			return nil, fmt.Errorf("invalid A record length")
		}
		out = append(out, netip.AddrFrom4([4]byte(r.Data[:4])).String())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no A answers")
	}
	return out, nil
}

func lookup(name string) result {
	var head *windows.DNSRecord
	err := windows.DnsQuery(name, windows.DNS_TYPE_A, queryOptions, nil, &head, nil)
	if head != nil {
		defer windows.DnsRecordListFree(head, 1)
	}
	var r result
	if err == nil {
		r.Addresses, err = addresses(head)
	}
	if err != nil {
		r.Error = err.Error()
	}
	return r
}

func main() {
	if len(os.Args) != 2 || len(os.Args[1]) == 0 || len(os.Args[1]) > 253 {
		fmt.Fprintln(os.Stderr, "usage: dnslookup.exe FQDN")
		os.Exit(2)
	}
	start := time.Now()
	done := make(chan result, 1)
	go func() { done <- lookup(os.Args[1]) }()
	var r result
	select {
	case r = <-done:
	case <-time.After(20 * time.Second):
		r.Error = "native DNS query exceeded 20 seconds"
	}
	r.DurationMS, r.BypassCache = time.Since(start).Milliseconds(), true
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil || r.Error != "" {
		os.Exit(1)
	}
}
