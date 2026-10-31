// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/platformjournal"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows/svc"
)

func startPlatformJournal(store *localapi.LogStore) (io.Closer, error) {
	service, err := svc.IsWindowsService()
	if err != nil || !service {
		return nil, err
	}
	paths, err := productruntime.WindowsDefaultPaths()
	if err != nil {
		return nil, err
	}
	root, err := winfile.OpenSystemDirectory(filepath.Dir(paths.Config))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	prefix := "daemon"
	for _, arg := range os.Args[1:] {
		if arg == "--boot-recover" {
			prefix = "recovery"
		}
	}
	return platformjournal.Start(store, filepath.Join(filepath.Dir(paths.Config), "logs"), prefix)
}
