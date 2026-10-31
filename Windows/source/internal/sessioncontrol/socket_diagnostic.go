// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"errors"
	"log/slog"
)

// LogListenDiagnostics emits bounded platform observations without changing the listen error.
func LogListenDiagnostics(logger *slog.Logger, err error) {
	var diagnostic interface{ logListenDiagnostics(*slog.Logger) }
	if logger != nil && errors.As(err, &diagnostic) {
		diagnostic.logListenDiagnostics(logger)
	}
}
