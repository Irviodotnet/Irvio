// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package controlsocket

import "errors"

var ErrInvalidMark = errors.New("invalid Irvio control socket mark")

var ErrInvalidDevice = errors.New("invalid Irvio control socket device")
