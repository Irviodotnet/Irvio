// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

// hydra-test-lab-dns-launcher replaces DNS commands only inside the child's isolated mount namespace.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func main() {
	// Namespace changes and exec must stay on the same Linux thread.
	runtime.LockOSThread()
	args := os.Args[1:]
	runtimeRoot := ""
	if len(args) >= 2 && args[0] == "--runtime-root" {
		runtimeRoot, args = args[1], args[2:]
		if !filepath.IsAbs(runtimeRoot) || !strings.HasPrefix(filepath.Clean(runtimeRoot), filepath.Join(os.TempDir(), "hydra-m4b-runtime-")) {
			fmt.Fprintln(os.Stderr, "runtime root must be an isolated M4b lab directory")
			os.Exit(2)
		}
		info, err := os.Lstat(runtimeRoot)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			fmt.Fprintln(os.Stderr, "runtime root must be a real owner-only directory")
			os.Exit(2)
		}
	}
	if len(args) == 0 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: irvio-test-lab-dns-launcher [--runtime-root directory] <irviod> [args...]")
		os.Exit(2)
	}
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		fmt.Fprintln(os.Stderr, "create lab mount namespace:", err)
		os.Exit(1)
	}
	if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		fmt.Fprintln(os.Stderr, "isolate lab mount propagation:", err)
		os.Exit(1)
	}
	if runtimeRoot != "" {
		if err := unix.Mount(runtimeRoot, "/run", "", unix.MS_BIND, ""); err != nil {
			fmt.Fprintln(os.Stderr, "isolate lab runtime state:", err)
			os.Exit(1)
		}
	}
	// Restore cgroup2 hidden by iproute2's sysfs remount; control ownership must use real cgroups.
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); errors.Is(err, os.ErrNotExist) {
		if err := unix.Mount("none", "/sys/fs/cgroup", "cgroup2", 0, ""); err != nil {
			fmt.Fprintln(os.Stderr, "restore lab cgroup2 mount:", err)
			os.Exit(1)
		}
	}
	if err := unix.Mount("/usr/bin/true", "/usr/bin/resolvectl", "", unix.MS_BIND, ""); err != nil {
		fmt.Fprintln(os.Stderr, "install lab-only DNS command:", err)
		os.Exit(1)
	}
	if err := syscall.Exec(args[0], args, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "exec hydrad in lab mount namespace:", err)
		os.Exit(1)
	}
}
