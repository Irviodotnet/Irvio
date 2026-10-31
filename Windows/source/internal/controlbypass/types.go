// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package controlbypass owns process-scoped physical-WAN routing for accepted control sockets.
package controlbypass

import (
	"errors"
	"os"
)

const (
	DefaultTableName = "hydra_control"
	OwnerMarker      = "hydra-control-bypass-v1"
)

var (
	ErrInvalidConfig = errors.New("invalid control bypass configuration")
	ErrUnsupported   = errors.New("cgroup v2 control bypass is unsupported")
	ErrConflict      = errors.New("control bypass ownership conflict")
)

type Config struct {
	Root        string
	Name        string
	TableName   string
	Mark        uint32
	PID         int
	ListenPorts []uint16
}

type Snapshot struct {
	CgroupPath  string
	CgroupID    uint64
	CgroupLevel uint32
	Mark        uint32
	Installed   bool
	Adopted     bool
	Packets     uint64
	Bytes       uint64
}

func withDefaults(config Config) Config {
	if config.Root == "" {
		config.Root = "/sys/fs/cgroup"
	}
	if config.TableName == "" {
		config.TableName = DefaultTableName
	}
	if config.PID == 0 {
		config.PID = os.Getpid()
	}
	return config
}
