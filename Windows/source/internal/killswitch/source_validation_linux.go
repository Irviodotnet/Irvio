// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package killswitch

import (
	"fmt"
	"os"
	"strings"
)

const sourceValidationPath = "/proc/sys/net/ipv4/conf/all/src_valid_mark"

func readSourceValidation() (bool, error) {
	payload, err := os.ReadFile(sourceValidationPath)
	if err != nil {
		return false, fmt.Errorf("read Irvio source-validation sysctl: %w", err)
	}
	switch strings.TrimSpace(string(payload)) {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("read Irvio source-validation sysctl: %w", ErrConflict)
	}
}

func writeSourceValidation(enabled bool) error {
	value := []byte("0")
	if enabled {
		value = []byte("1")
	}
	if err := os.WriteFile(sourceValidationPath, value, 0); err != nil {
		return fmt.Errorf("write Irvio source-validation sysctl: %w", err)
	}
	return nil
}

func sourceValidationLabel(previous bool) string {
	if previous {
		return sourceValidationPreviousOne
	}
	return sourceValidationPreviousZero
}
