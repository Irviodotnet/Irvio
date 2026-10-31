// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"

	"github.com/vishvananda/netlink"
)

func main() {
	flush := flag.Bool("flush", false, "flush the current network namespace conntrack table")
	udpPeer := flag.String("delete-udp-peer", "", "delete only UDP flows involving this internal IPv4 peer")
	flag.Parse()
	if flag.NArg() != 0 || (*flush && *udpPeer != "") || (!*flush && *udpPeer == "") {
		fmt.Fprintln(os.Stderr, "usage: irvio-test-conntrack --flush | --delete-udp-peer <IPv4>")
		os.Exit(2)
	}
	if *udpPeer != "" {
		addr, err := netip.ParseAddr(*udpPeer)
		if err != nil || !addr.Is4() || !addr.IsGlobalUnicast() {
			fmt.Fprintln(os.Stderr, "delete-udp-peer requires a unicast IPv4 address")
			os.Exit(2)
		}
		var filters []netlink.CustomConntrackFilter
		for _, field := range []netlink.ConntrackFilterType{netlink.ConntrackOrigSrcIP, netlink.ConntrackReplySrcIP} {
			filter := &netlink.ConntrackFilter{}
			if err := filter.AddIP(field, net.IP(addr.AsSlice())); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if err := filter.AddProtocol(17); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			filters = append(filters, filter)
		}
		count, err := netlink.ConntrackDeleteFilters(netlink.ConntrackTable, netlink.FAMILY_V4, filters...)
		if err != nil {
			fmt.Fprintln(os.Stderr, "delete scoped UDP conntrack:", err)
			os.Exit(1)
		}
		fmt.Println(count)
		return
	}
	if err := netlink.ConntrackTableFlush(netlink.ConntrackTable); err != nil {
		fmt.Fprintf(os.Stderr, "flush conntrack table: %v\n", err)
		os.Exit(1)
	}
}
