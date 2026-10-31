// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

//go:embed bundle/*
var bundled embed.FS

var manifestSHA string
var development = "false"

var installerScripts = []string{"install-or-update", "install-or-update-server", "install-desktop", "install-server", "server-installation", "update-desktop", "recover-update", "software-update", "maintenance", "installation", "payload", "uninstall-for-user", "uninstall-desktop", "uninstall"}

var safeBundlePath = regexp.MustCompile(`^(payload/(payload-manifest\.json|(daemon|ui|driver|geo|licenses)/[A-Za-z0-9@_./+ -]+)|scripts/(install-or-update|install-or-update-server|install-desktop|install-server|server-installation|update-desktop|recover-update|software-update|maintenance|installation|payload|uninstall-for-user|uninstall-desktop|uninstall)\.ps1)$`)

func bundlePath(name string) bool {
	if !safeBundlePath.MatchString(name) || strings.Contains(name, "//") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "." || part == ".." || strings.TrimRight(part, ". ") != part {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
			return false
		}
	}
	return true
}

func readBundle(data []byte, digest string) (*zip.Reader, error) {
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return nil, errors.New("installer has no valid embedded manifest digest; use build-setup.ps1")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if len(zr.File) == 0 || len(zr.File) > 20001+len(installerScripts) {
		return nil, errors.New("invalid installer file count")
	}
	seen := make(map[string]bool)
	var total uint64
	for _, file := range zr.File {
		key := strings.ToLower(file.Name)
		if !bundlePath(file.Name) || seen[key] || file.Mode()&fs.ModeType != 0 || file.UncompressedSize64 > 512<<20 {
			return nil, fmt.Errorf("unsafe installer entry: %q", file.Name)
		}
		seen[key] = true
		total += file.UncompressedSize64
		if total > 1<<30 {
			return nil, errors.New("installer payload exceeds size limit")
		}
		if file.Name == "payload/payload-manifest.json" {
			if file.UncompressedSize64 > 4<<20 {
				return nil, errors.New("installer manifest exceeds size limit")
			}
			reader, err := file.Open()
			if err != nil {
				return nil, err
			}
			hash := sha256.New()
			_, copyErr := io.Copy(hash, io.LimitReader(reader, (4<<20)+1))
			if err := errors.Join(copyErr, reader.Close()); err != nil {
				return nil, err
			}
			if hex.EncodeToString(hash.Sum(nil)) != digest {
				return nil, errors.New("embedded manifest digest mismatch")
			}
		}
	}
	required := []string{"payload/payload-manifest.json"}
	for _, name := range installerScripts {
		required = append(required, "scripts/"+name+".ps1")
	}
	for _, name := range required {
		if !seen[name] {
			return nil, fmt.Errorf("installer component missing: %s", name)
		}
	}
	return zr, nil
}

func extractBundle(zr *zip.Reader, root string) (result error) {
	directories := make(map[string]*winfile.Directory)
	defer func() {
		for _, directory := range directories {
			result = errors.Join(result, directory.Close())
		}
	}()
	for _, entry := range zr.File {
		parts := strings.Split(entry.Name, "/")
		parent := root
		for _, part := range parts[:len(parts)-1] {
			parent = filepath.Join(parent, part)
			if directories[parent] == nil {
				dir, err := winfile.OpenDirectory(parent, true)
				if err != nil {
					return err
				}
				directories[parent] = dir
			}
		}
		file, err := directories[parent].OpenFile(parts[len(parts)-1], true, true)
		if err != nil {
			return err
		}
		reader, err := entry.Open()
		if err != nil {
			return errors.Join(err, file.Close())
		}
		written, copyErr := io.Copy(file, io.LimitReader(reader, int64(entry.UncompressedSize64)+1))
		result = errors.Join(copyErr, reader.Close(), file.Sync(), file.Close())
		if result != nil {
			return result
		}
		if written != int64(entry.UncompressedSize64) {
			return errors.New("installer entry size mismatch")
		}
	}
	return nil
}

func loadBundle() (*zip.Reader, error) {
	data, err := bundled.ReadFile("bundle/payload.zip")
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("this executable is not a packaged IRVIO installer; run build-setup.ps1")
	}
	if err != nil {
		return nil, err
	}
	return readBundle(data, manifestSHA)
}
