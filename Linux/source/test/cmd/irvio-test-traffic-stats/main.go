// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

// Local-only acceptance probe; never prints identities or destinations.
package main

import (
	"context"
	"fmt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"github.com/Irviodotnet/Irvio/internal/localapi"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		return fmt.Errorf("usage: irvio-test-traffic-stats SOCKET stats|status|connect COUNTRY|disconnect")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c, err := localapi.Dial(ctx, os.Args[1])
	if err != nil {
		return err
	}
	defer c.Close()
	var message proto.Message
	switch os.Args[2] {
	case "stats":
		message, err = c.GetTrafficStats(ctx)
	case "status":
		message, err = c.GetStatus(ctx)
	case "connect":
		if len(os.Args) != 4 {
			return fmt.Errorf("country required")
		}
		err = c.Connect(ctx, os.Args[3])
	case "disconnect":
		err = c.Disconnect(ctx)
	default:
		return fmt.Errorf("unknown action")
	}
	if err != nil {
		return err
	}
	if message != nil {
		data, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(message)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	}
	return nil
}
