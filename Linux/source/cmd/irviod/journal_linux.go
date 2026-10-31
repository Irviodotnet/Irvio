// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"io"

	"github.com/Irviodotnet/Irvio/internal/localapi"
)

func startPlatformJournal(*localapi.LogStore) (io.Closer, error) { return nil, nil }
