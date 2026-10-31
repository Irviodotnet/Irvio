// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import "github.com/Irviodotnet/Irvio/internal/discovery"

func validatePublicRoleSocketPolicy(config PublicRoleConfig, node *discovery.Node) error {
	if config.ControlDialer != nil && node.ControlMark() != 0 {
		return ErrInvalidConfig
	}
	return nil
}
