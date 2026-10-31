// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (result error) {
	owner, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-DNS", Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.245.1/24"), netip.MustParsePrefix("fd77:245::1/64")}})
	if err != nil {
		return err
	}
	defer func() {
		if err := owner.Close(); result == nil {
			result = err
		}
	}()
	command := exec.Command(`C:\IRVIO-Lab\evidence\w05-dns\dns-layout.exe`, "owned-lab-change")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}
