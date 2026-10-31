// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"fmt"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
)

func configureLocalAPIServer(config localapi.ServerConfig, controller localapi.RuntimeController, groupName string) (*localapi.Server, error) {
	if groupName != "" && groupName != "irvio" {
		return nil, fmt.Errorf("Windows Local API uses the installed account SID, not a Unix group")
	}
	sid, err := productruntime.WindowsLocalAPIUserSID()
	if err != nil {
		return nil, err
	}
	policy, err := localapi.NewWindowsCredentialPolicy(sid)
	if err != nil {
		return nil, fmt.Errorf("invalid installed desktop identity: %w", err)
	}
	config.Authorizer = policy
	return localapi.NewServer(config, controller)
}
