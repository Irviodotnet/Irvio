// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sharecli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestInvitationOwnerRejectsInvalidSudoMetadata(t *testing.T) {
	for _, values := range []map[string]string{
		{"SUDO_UID": "-1"}, {"SUDO_UID": "4294967295"},
		{"SUDO_UID": "0", "SUDO_GID": "-1"},
		{"SUDO_UID": "0", "SUDO_GID": "0", "SUDO_USER": "not-root"},
	} {
		env := func(key string) string { return values[key] }
		if _, _, err := invitationOwner(0, env); err == nil {
			t.Fatal("accepted invalid ownership metadata")
		}
		if uid, gid, err := invitationOwner(1000, env); err != nil || uid != -1 || gid != -1 {
			t.Fatal("trusted sudo metadata without root")
		}
	}
}

func TestInvitationOwnerAccessDespiteUmask(t *testing.T) {
	directory := t.TempDir()
	previous := syscall.Umask(0777)
	file, err := createInvitationFile(directory)
	syscall.Umask(previous)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
}

func TestSudoInvitationCanBeOpenedAndCopiedByInvokingUser(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("SUDO_UID") == "" || os.Getenv("SUDO_UID") == "0" {
		t.Skip("requires sudo from an ordinary user")
	}
	uid, gid, err := invitationOwner(0, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("/tmp", "irvio-invitation-access-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	if err := os.Chown(directory, uid, gid); err != nil {
		t.Fatal(err)
	}
	file, err := createInvitationFile(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("test invitation\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"/bin/cat", file.Name()}, {"/bin/cp", file.Name(), filepath.Join(directory, "copied.txt")}} {
		command := exec.Command(args[0], args[1:]...)
		command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("UID %s: %v %s", strconv.Itoa(uid), err, output)
		}
	}
}
