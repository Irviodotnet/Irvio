// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package controlbypass

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var validName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type Ownership struct {
	config  Config
	parent  string
	path    string
	id      uint64
	level   uint32
	nft     *nftOwnership
	adopted bool

	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func Install(config Config) (_ *Ownership, resultErr error) {
	config = withDefaults(config)
	if !validName.MatchString(config.Name) || !validName.MatchString(config.TableName) || config.Mark == 0 || config.PID <= 0 {
		return nil, ErrInvalidConfig
	}
	if filesystem, err := os.Stat(filepath.Join(config.Root, "cgroup.controllers")); err != nil || filesystem.IsDir() {
		return nil, ErrUnsupported
	}
	relative, err := currentCgroup(config.PID)
	if err != nil {
		return nil, err
	}
	if filepath.Base(relative) == config.Name {
		relative = filepath.Dir(relative)
	}
	parent := filepath.Join(config.Root, filepath.Clean("/"+relative))
	path := filepath.Join(parent, config.Name)
	created := false
	if err := os.Mkdir(path, 0o755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create Irvio control cgroup: %w", err)
		}
	} else {
		created = true
	}
	if !created {
		members, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return nil, err
		}
		for _, member := range strings.Fields(string(members)) {
			if member != strconv.Itoa(config.PID) {
				return nil, ErrConflict
			}
		}
	}
	owner := &Ownership{config: config, parent: parent, path: path, adopted: !created}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, owner.rollback(created))
		}
	}()
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return nil, ErrUnsupported
	}
	owner.id = stat.Ino
	owner.level = cgroupLevel(config.Root, path)
	owner.nft, err = ensureNFT(config.TableName, config.Mark, owner.id, owner.level, config.ListenPorts)
	if err != nil {
		return nil, err
	}
	if err := writePID(filepath.Join(path, "cgroup.procs"), config.PID); err != nil {
		return nil, fmt.Errorf("join Irvio control cgroup: %w", err)
	}
	return owner, nil
}

func currentCgroup(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			value := strings.TrimPrefix(line, "0::")
			if value == "" {
				value = "/"
			}
			return value, nil
		}
	}
	return "", ErrUnsupported
}

func cgroupLevel(root, path string) uint32 {
	relative, _ := filepath.Rel(root, path)
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	var level uint32
	for _, part := range parts {
		if part != "" && part != "." {
			level++
		}
	}
	return level
}

func writePID(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600)
}

func (o *Ownership) Snapshot() (Snapshot, error) {
	if o == nil {
		return Snapshot{}, ErrInvalidConfig
	}
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		return Snapshot{CgroupPath: o.path, CgroupID: o.id, CgroupLevel: o.level, Mark: o.config.Mark}, nil
	}
	packets, bytes, err := o.nft.counter()
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		CgroupPath: o.path, CgroupID: o.id, CgroupLevel: o.level, Mark: o.config.Mark,
		Installed: true, Adopted: o.adopted, Packets: packets, Bytes: bytes,
	}, nil
}

func (o *Ownership) rollback(removeDirectory bool) error {
	var result error
	if o.nft != nil {
		result = errors.Join(result, o.nft.remove())
	}
	if o.parent != "" {
		result = errors.Join(result, writePID(filepath.Join(o.parent, "cgroup.procs"), o.config.PID))
	}
	if removeDirectory && o.path != "" {
		result = errors.Join(result, os.Remove(o.path))
	}
	return result
}

func (o *Ownership) Close() error {
	if o == nil {
		return nil
	}
	o.closeOnce.Do(func() {
		o.mu.Lock()
		o.closed = true
		o.mu.Unlock()
		o.closeErr = o.rollback(true)
	})
	return o.closeErr
}
