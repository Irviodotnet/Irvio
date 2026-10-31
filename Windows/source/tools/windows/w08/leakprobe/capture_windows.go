// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type captureReport struct {
	Result            string   `json:"result"`
	ProtectedAttempts [2]int   `json:"protected_attempts"`
	ProtectedSpanMS   [2]int64 `json:"protected_span_ms"`
	MaximumGapMS      [2]int64 `json:"maximum_gap_ms"`
	BaselinePackets   [2]int   `json:"baseline_packets"`
	AfterPackets      [2]int   `json:"after_packets"`
	PhysicalLeaks     int      `json:"physical_leaks"`
}

func analyze(log io.Reader, captures []io.Reader, runID string) (captureReport, error) {
	var report captureReport
	if len(captures) != 2 || runID == "" {
		return report, fmt.Errorf("two captures and run ID required")
	}
	rank := map[string]int{"baseline": 0, "protected": 1, "teardown": 2, "after": 3}
	var rows []attempt
	lastPhase := 0
	lastTime := int64(0)
	var first, last [2]int64
	scanner := bufio.NewScanner(log)
	for scanner.Scan() {
		var row attempt
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return report, err
		}
		p, ok := rank[row.Phase]
		if !ok || p < lastPhase || p > lastPhase+1 || row.Lane < 0 || row.Lane > 1 || row.Sequence != uint64(len(rows)+1) || row.ElapsedMS < lastTime || len(rows) >= 20000 {
			return report, fmt.Errorf("invalid or incomplete sender log")
		}
		lastPhase, lastTime = p, row.ElapsedMS
		rows = append(rows, row)
		if row.Phase == "protected" {
			lane := row.Lane
			if report.ProtectedAttempts[lane] == 0 {
				first[lane] = row.ElapsedMS
			} else if gap := row.ElapsedMS - last[lane]; gap > report.MaximumGapMS[lane] {
				report.MaximumGapMS[lane] = gap
			}
			last[lane] = row.ElapsedMS
			report.ProtectedAttempts[lane]++
		}
	}
	if err := scanner.Err(); err != nil {
		return report, err
	}
	for lane := range 2 {
		report.ProtectedSpanMS[lane] = last[lane] - first[lane]
		if lastPhase != 3 || report.ProtectedAttempts[lane] < 20 || report.ProtectedSpanMS[lane] < 5000 || report.MaximumGapMS[lane] > 1000 {
			return report, fmt.Errorf("insufficient continuous sender coverage")
		}
	}
	for index, capture := range captures {
		seen := make(map[uint64]bool)
		err := readCapture(capture, func(payload string) error {
			prefix := "irvio-w08-leak-v1|" + runID + "|"
			if !strings.HasPrefix(payload, prefix) {
				return nil
			}
			fields := strings.Split(payload, "|")
			if len(fields) != 5 {
				return fmt.Errorf("invalid oracle payload")
			}
			lane, err := strconv.Atoi(fields[2])
			if err != nil {
				return err
			}
			seq, err := strconv.ParseUint(fields[4], 10, 64)
			if err != nil {
				return err
			}
			if seq == 0 || seq > uint64(len(rows)) {
				return fmt.Errorf("unlogged packet")
			}
			row := rows[seq-1]
			if row.Lane != lane || row.Phase != fields[3] {
				return fmt.Errorf("packet/log mismatch")
			}
			if seen[seq] {
				return nil
			}
			seen[seq] = true
			if row.Phase == "protected" {
				report.PhysicalLeaks++
			}
			if lane == index && row.Phase == "baseline" {
				report.BaselinePackets[index]++
			}
			if lane == index && row.Phase == "after" {
				report.AfterPackets[index]++
			}
			return nil
		})
		if err != nil {
			return report, err
		}
		if report.BaselinePackets[index] < 3 || report.AfterPackets[index] < 3 {
			return report, fmt.Errorf("capture %d missing positive controls", index)
		}
	}
	report.Result = "PASS"
	if report.PhysicalLeaks != 0 {
		report.Result = "FAIL"
	}
	return report, nil
}

func readCapture(r io.Reader, visit func(string) error) error {
	header := make([]byte, 24)
	if _, err := io.ReadFull(r, header); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(header) != 0xa1b2c3d4 || binary.LittleEndian.Uint32(header[20:]) != 1 {
		return fmt.Errorf("unsupported PCAP")
	}
	var total uint64
	for {
		record := make([]byte, 16)
		if _, err := io.ReadFull(r, record); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		size := binary.LittleEndian.Uint32(record[8:])
		total += uint64(size)
		if size > 65536 || size != binary.LittleEndian.Uint32(record[12:]) || total > 128<<20 {
			return fmt.Errorf("oversized or truncated capture")
		}
		frame := make([]byte, size)
		if _, err := io.ReadFull(r, frame); err != nil {
			return err
		}
		if len(frame) < 34 || binary.BigEndian.Uint16(frame[12:]) != 0x800 {
			continue
		}
		ip := frame[14:]
		if ip[0]>>4 != 4 || ip[9] != 17 || string(ip[16:20]) != string([]byte{198, 51, 100, 77}) {
			continue
		}
		hlen := int(ip[0]&15) * 4
		length := int(binary.BigEndian.Uint16(ip[2:]))
		if hlen < 20 || length < hlen+8 || length > len(ip) || binary.BigEndian.Uint16(ip[6:])&0x3fff != 0 {
			return fmt.Errorf("invalid or fragmented oracle packet")
		}
		udp := ip[hlen:length]
		if binary.BigEndian.Uint16(udp[2:]) != 49199 {
			continue
		}
		if int(binary.BigEndian.Uint16(udp[4:])) != len(udp) {
			return fmt.Errorf("invalid UDP size")
		}
		if err := visit(string(udp[8:])); err != nil {
			return err
		}
	}
}

func check(args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("usage: leakprobe.exe check sender.ndjson nic0.pcap nic1.pcap run-id")
	}
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	for _, name := range args[:3] {
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		files = append(files, f)
	}
	report, err := analyze(files[0], []io.Reader{files[1], files[2]}, args[3])
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return err
	}
	if report.Result != "PASS" {
		return fmt.Errorf("cleartext protected probe escaped")
	}
	return nil
}
