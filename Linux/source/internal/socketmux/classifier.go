// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import "net/netip"

type dataClassifier struct{}

func (dataClassifier) Classify([]byte, netip.AddrPort, AddressFamily) PacketClass {
	return Data
}
