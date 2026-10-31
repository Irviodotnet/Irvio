// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"testing"
)

func fixture(leak, skipAfter bool) ([]byte, [2][]byte) {
	var log bytes.Buffer
	var packets [2]bytes.Buffer
	for lane := range 2 {
		h := make([]byte, 24)
		binary.LittleEndian.PutUint32(h, 0xa1b2c3d4)
		binary.LittleEndian.PutUint32(h[20:], 1)
		packets[lane].Write(h)
	}
	seq := uint64(0)
	for _, phase := range []string{"baseline", "protected", "teardown", "after"} {
		count := 3
		if phase == "protected" {
			count = 60
		}
		for i := 0; i < count; i++ {
			for lane := range 2 {
				seq++
				row := attempt{Sequence: seq, ElapsedMS: int64(seq) * 100, Lane: lane, Phase: phase}
				json.NewEncoder(&log).Encode(row)
				if phase == "baseline" || (phase == "after" && !skipAfter) || (phase == "protected" && leak && i == 0 && lane == 0) {
					payload := []byte(fmt.Sprintf("irvio-w08-leak-v1|test|%d|%s|%d", lane, phase, seq))
					frame := make([]byte, 42+len(payload))
					frame[12] = 8
					frame[14] = 0x45
					frame[23] = 17
					binary.BigEndian.PutUint16(frame[16:], uint16(len(frame)-14))
					copy(frame[30:34], []byte{198, 51, 100, 77})
					binary.BigEndian.PutUint16(frame[36:], 49199)
					binary.BigEndian.PutUint16(frame[38:], uint16(8+len(payload)))
					copy(frame[42:], payload)
					record := make([]byte, 16)
					binary.LittleEndian.PutUint32(record[8:], uint32(len(frame)))
					binary.LittleEndian.PutUint32(record[12:], uint32(len(frame)))
					packets[lane].Write(record)
					packets[lane].Write(frame)
				}
			}
		}
	}
	return log.Bytes(), [2][]byte{packets[0].Bytes(), packets[1].Bytes()}
}

func TestCaptureOracle(t *testing.T) {
	for _, tc := range []struct {
		name          string
		leak, missing bool
		result        string
		invalid       bool
	}{
		{"clean", false, false, "PASS", false}, {"leak", true, false, "FAIL", false}, {"missing-positive", false, true, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log, pcaps := fixture(tc.leak, tc.missing)
			got, err := analyze(bytes.NewReader(log), []io.Reader{bytes.NewReader(pcaps[0]), bytes.NewReader(pcaps[1])}, "test")
			if (err != nil) != tc.invalid || got.Result != tc.result {
				t.Fatal(got, err)
			}
			if tc.leak && got.PhysicalLeaks != 1 {
				t.Fatal(got)
			}
		})
	}
}

func TestIncompleteEvidenceRejected(t *testing.T) {
	log, pcaps := fixture(false, false)
	for _, tc := range []struct {
		name      string
		log, pcap []byte
		run       string
	}{
		{"truncated-pcap", log, pcaps[0][:len(pcaps[0])-1], "test"},
		{"wrong-run", log, pcaps[0], "other"},
		{"missing-sender", nil, pcaps[0], "test"},
		{"missing-sequence", bytes.Replace(log, []byte(`"sequence":1,`), []byte(`"sequence":2,`), 1), pcaps[0], "test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := analyze(bytes.NewReader(tc.log), []io.Reader{bytes.NewReader(tc.pcap), bytes.NewReader(pcaps[1])}, tc.run); err == nil {
				t.Fatal("incomplete evidence accepted")
			}
		})
	}
}
