// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productprofile

import (
	"errors"
	"strings"
)

const (
	MarkerFilename = ".hydra-profile-v1"
	// Preserve the v1 ownership marker bytes for profile compatibility across rebranding.
	markerContents = "HYDRA product profile v1\n"
)

var (
	ErrInvalidPath     = errors.New("invalid Irvio profile path")
	ErrProfileNotOwned = errors.New("directory is not an owned Irvio profile")
	ErrProfileChanged  = errors.New("Irvio profile directory changed")
)

func knownProfileEntry(name string) bool {
	switch name {
	case "identity.key", "awg-identity", "announcement-incarnation",
		"hydra.db", "hydra.db-wal", "hydra.db-shm", "hydra.db-journal",
		"bootstrap-v1.pb", "exit-pool-v1.json", "listen-port-v1.json",
		"private-access-v1.json", ".private-access-v1.lock", "persistent-sharing-v1.json":
		return true
	}
	for _, prefix := range []string{
		".identity-", ".awg-identity-", ".bootstrap-v1-", ".exit-pool-v1-", ".hydra-profile-marker-",
		".private-access-v1-", ".session-state-",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
