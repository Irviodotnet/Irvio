// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"

	"github.com/Irviodotnet/Irvio/internal/localapi"
)

func configureLocalAPIServer(config localapi.ServerConfig, controller localapi.RuntimeController, groupName string) (*localapi.Server, error) {
	if os.Geteuid() != 0 {
		return localapi.NewServer(config, controller)
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return nil, fmt.Errorf("resolve local API group %q: %w", groupName, err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil || gid < 0 {
		return nil, fmt.Errorf("invalid local API group ID %q", group.Gid)
	}
	config.OwnerUID = 0
	config.OwnerGID = gid
	selectedUID := os.Getenv("IRVIO_LOCAL_API_UID")
	if selectedUID != "" && (os.Getenv("LISTEN_FDS") != "1" || os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid())) {
		return nil, errors.New("IRVIO_LOCAL_API_UID requires systemd socket activation")
	}
	policy, err := localAPICredentialPolicy(selectedUID, uint32(gid))
	if err != nil {
		return nil, err
	}
	config.Authorizer = policy
	return localapi.NewServer(config, controller)
}

// Packaged desktops authorize the selected UID, independent of login-session groups.
func localAPICredentialPolicy(selectedUID string, gid uint32) (localapi.CredentialPolicy, error) {
	policy := localapi.CredentialPolicy{AllowedUIDs: map[uint32]struct{}{0: {}}}
	if selectedUID == "" {
		policy.AllowedGIDs = map[uint32]struct{}{gid: {}}
		return policy, nil
	}
	uid, err := strconv.ParseUint(selectedUID, 10, 32)
	if err != nil || uid < 1000 || uid == 1<<32-1 || strconv.FormatUint(uid, 10) != selectedUID {
		return localapi.CredentialPolicy{}, fmt.Errorf("invalid desktop Local API UID %q", selectedUID)
	}
	policy.AllowedUIDs[uint32(uid)] = struct{}{}
	return policy, nil
}
