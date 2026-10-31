// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"regexp"

	"github.com/Irviodotnet/Irvio/internal/linuxroute"
)

var validObjectName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

// normalizePolicy preserves policy validation independently of the OS backend.
func normalizePolicy(policy Policy) (Policy, error) {
	if policy.TableName == "" {
		policy.TableName = DefaultTableName
	}
	if policy.TunnelControlMark == 0 {
		policy.TunnelControlMark = linuxroute.IrvioTunnelControlMark
	}
	if !validObjectName.MatchString(policy.TableName) || !validObjectName.MatchString(policy.TUNName) ||
		policy.BypassMark == 0 || policy.TunnelControlMark == 0 || policy.TunnelControlMark == policy.BypassMark {
		return Policy{}, ErrInvalidConfig
	}
	return policy, nil
}
