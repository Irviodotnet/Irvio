// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnel

import "net/netip"

func normalizeConfig(config Config) (Config, error) {
	if config.Name == "" {
		config.Name = DefaultName
	}
	if config.MTU == 0 {
		config.MTU = DefaultMTU
	}
	if len(config.Name) > 15 || config.MTU < 576 || config.MTU > 65535 || len(config.Addresses) == 0 || len(config.Addresses) > 4 {
		return Config{}, ErrInvalidConfig
	}
	seen := make(map[string]struct{}, len(config.Addresses))
	for index, prefix := range config.Addresses {
		if !prefix.IsValid() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() || prefix.Addr().Is4In6() {
			return Config{}, ErrInvalidConfig
		}
		config.Addresses[index] = prefix
		if _, exists := seen[prefix.String()]; exists {
			return Config{}, ErrInvalidConfig
		}
		seen[prefix.String()] = struct{}{}
	}
	return config, nil
}

func validDynamicPrefix(prefix netip.Prefix) bool {
	return prefix.IsValid() && prefix.Bits() == prefix.Addr().BitLen() &&
		!prefix.Addr().IsUnspecified() && !prefix.Addr().IsMulticast() && !prefix.Addr().Is4In6()
}
