// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	monitor, err := networkstate.NewMonitor(networkstate.Config{})
	if err != nil {
		return err
	}
	defer monitor.Close()
	baseline := monitor.Current()
	device, err := tun.CreateTUN("IRVIO-NETSTATE", 1280)
	if err != nil {
		return err
	}
	defer device.Close()
	go func() {
		for range device.Events() {
		}
	}()
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(map[string]any{"stage": "ready", "baseline": baseline, "luid": device.(*tun.NativeTun).LUID()}); err != nil {
		return err
	}
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		select {
		case err := <-monitor.Errors():
			return fmt.Errorf("monitor terminated: %v", err)
		default:
		}
		switch input.Text() {
		case "state":
			if err := encoder.Encode(monitor.Current()); err != nil {
				return err
			}
		case "close-adapter":
			if err := device.Close(); err != nil {
				return err
			}
			if err := encoder.Encode(map[string]any{"stage": "adapter_closed"}); err != nil {
				return err
			}
		case "quit":
			return monitor.Close()
		default:
			return fmt.Errorf("unknown lab command")
		}
	}
	return input.Err()
}
