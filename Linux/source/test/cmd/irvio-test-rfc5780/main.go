// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Command hydra-test-rfc5780 runs the M4b test-only responder.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Irviodotnet/Irvio/test/support/rfc5780"
)

type readiness struct {
	Type     string           `json:"type"`
	PID      int              `json:"pid"`
	Topology rfc5780.Topology `json:"topology"`
}

func main() {
	if err := run(); err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "error", "error": err.Error()})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	a1Text := flag.String("a1", "", "primary server IP address")
	a2Text := flag.String("a2", "", "other server IP address")
	p1 := flag.Int("p1", 3478, "primary UDP port")
	p2 := flag.Int("p2", 3479, "alternate UDP port")
	observationsPath := flag.String("observations", "", "JSON-lines observation path")
	snapshotPath := flag.String("snapshot", "", "final JSON observation snapshot path")
	readyPath := flag.String("ready-file", "", "atomic JSON readiness file")
	dropFirst := flag.Int("drop-first", 0, "drop the first N requests")
	dropChangePort := flag.Bool("drop-change-port", false, "drop change-port responses")
	dropChangeIPPort := flag.Bool("drop-change-ip-port", false, "drop change-IP-and-port responses")
	unknownChange := flag.Bool("unknown-change-request", false, "return Binding error 420 for CHANGE-REQUEST")
	delay := flag.Duration("response-delay", 0, "deterministic response delay")
	baselineReady := flag.String("baseline-ready-file", "", "file created after the baseline response")
	baselineContinue := flag.String("baseline-continue-file", "", "file releasing the baseline barrier")
	flag.Parse()

	a1, err := netip.ParseAddr(*a1Text)
	if err != nil {
		return fmt.Errorf("parse --a1: %w", err)
	}
	a2, err := netip.ParseAddr(*a2Text)
	if err != nil {
		return fmt.Errorf("parse --a2: %w", err)
	}
	if *p1 < 1 || *p1 > 65535 || *p2 < 1 || *p2 > 65535 {
		return errors.New("--p1 and --p2 must be within 1..65535")
	}

	var observationFile *os.File
	if *observationsPath != "" {
		observationFile, err = os.OpenFile(*observationsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open observation file: %w", err)
		}
		defer observationFile.Close()
	}
	var outputMu sync.Mutex
	hook := func(observation rfc5780.Observation) {
		if observationFile == nil {
			return
		}
		outputMu.Lock()
		defer outputMu.Unlock()
		writer := bufio.NewWriterSize(observationFile, 4096)
		if err := json.NewEncoder(writer).Encode(observation); err == nil {
			_ = writer.Flush()
		}
	}

	server, err := rfc5780.New(rfc5780.Config{
		PrimaryIP: a1, OtherIP: a2, PrimaryPort: uint16(*p1), OtherPort: uint16(*p2),
		ObservationHook: hook,
		Script: rfc5780.Script{
			DropFirst: *dropFirst, DropChangePort: *dropChangePort,
			DropChangeIPAndPort: *dropChangeIPPort, UnknownChangeRequest: *unknownChange,
			ResponseDelay: *delay, BaselineReadyFile: *baselineReady,
			BaselineContinueFile: *baselineContinue,
		},
	})
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	ready := readiness{Type: "ready", PID: os.Getpid(), Topology: server.Topology()}
	if *readyPath != "" {
		if err := writeJSON(*readyPath, ready); err != nil {
			_ = server.Close()
			return err
		}
	}
	if err := encoder.Encode(ready); err != nil {
		_ = server.Close()
		return fmt.Errorf("write readiness: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	closeErr := server.Close()
	observations, backgroundErr := server.Snapshot()
	if *snapshotPath != "" {
		if err := writeJSON(*snapshotPath, observations); err != nil {
			return err
		}
	}
	if closeErr != nil {
		return closeErr
	}
	return backgroundErr
}

func writeJSON(path string, value any) error {
	temporary := path + fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create JSON temporary file: %w", err)
	}
	encodeErr := json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if encodeErr != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("encode JSON: %w", encodeErr)
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("close JSON: %w", closeErr)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish JSON: %w", err)
	}
	return nil
}
