// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/uibridge"
)

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "IRVIO UI bridge accepts no arguments")
		os.Exit(2)
	}
	conn, err := localapi.DialDesktopTransport(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "IRVIO Local API unavailable:", err)
		os.Exit(1)
	}
	if err := uibridge.Relay(conn, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "IRVIO Local API relay closed")
		os.Exit(1)
	}
}
