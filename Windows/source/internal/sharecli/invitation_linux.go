// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sharecli

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

// Resolve sudo's invoking account without trusting ownership hints from unprivileged callers.
func invitationOwner(euid int, env func(string) string) (int, int, error) {
	if euid != 0 || env("SUDO_UID") == "" && env("SUDO_GID") == "" && env("SUDO_USER") == "" {
		return -1, -1, nil
	}
	uid, err := strconv.Atoi(env("SUDO_UID"))
	if err != nil || uid < 0 || uint64(uid) >= 1<<32-1 {
		return 0, 0, fmt.Errorf("invalid sudo user ID; run irvio as your ordinary user")
	}
	gid, err := strconv.Atoi(env("SUDO_GID"))
	if err != nil || gid < 0 || uint64(gid) >= 1<<32-1 {
		return 0, 0, fmt.Errorf("invalid sudo group ID; run irvio as your ordinary user")
	}
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil || account.Username != env("SUDO_USER") || account.Gid != strconv.Itoa(gid) {
		return 0, 0, fmt.Errorf("cannot verify sudo's invoking account; run irvio as your ordinary user")
	}
	return uid, gid, nil
}

func createInvitationFile(directory string) (*os.File, error) {
	uid, gid, err := invitationOwner(os.Geteuid(), os.Getenv)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(directory, "irvio-invitation-*.txt")
	if err != nil {
		return nil, err
	}
	if uid >= 0 {
		err = file.Chown(uid, gid)
	}
	if err == nil {
		err = file.Chmod(0600)
	}
	if err != nil {
		file.Close()
		os.Remove(file.Name())
		return nil, fmt.Errorf("set invitation owner/read permissions: %w", err)
	}
	return file, nil
}
