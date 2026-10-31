//go:build linux

// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"time"
)

type streamProbeReport struct {
	Attempts, Successes, Failures, WrongEgress int
	Completed                                  bool
}

func streamProbe(config options) (resultErr error) {
	want, err := netip.ParseAddr(config.expectedEgress)
	if err != nil || !want.IsValid() || want.Is4In6() || config.result == "" || config.ready == "" || config.release == "" ||
		config.probeTimeout <= 0 || config.probeTimeout > time.Second || config.socketMark != 0 || config.bindDevice != "" {
		return errors.New("stream-probe requires bounded unmarked probes, expected egress and artifact paths")
	}
	if _, err := netip.ParseAddrPort(config.target); err != nil {
		return errors.New("stream-probe requires a literal target endpoint")
	}
	var report streamProbeReport
	defer func() {
		data, err := json.Marshal(report)
		if err == nil {
			err = writeAtomic(config.result, append(data, '\n'))
		}
		resultErr = errors.Join(resultErr, err)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for {
		if _, err := os.Stat(config.release); err == nil {
			if report.Successes == 0 {
				return errors.New("stream-probe stopped without any successful traffic")
			}
			report.Completed = true
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		attempt, stop := context.WithTimeout(ctx, config.probeTimeout)
		probe, err := probeUDP(attempt, config.target, 32, probeDialer(config))
		stop()
		report.Attempts++
		if err != nil {
			report.Failures++
		} else {
			observed, err := netip.ParseAddrPort(probe.ObservedSource)
			if err != nil || observed.Addr() != want {
				report.WrongEgress++
				return fmt.Errorf("stream-probe observed forbidden egress %q", probe.ObservedSource)
			}
			report.Successes++
			if report.Successes == 1 {
				if err := writeAtomic(config.ready, []byte("ready\n")); err != nil {
					return err
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
