// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"encoding/binary"

	"github.com/pion/stun/v3"
)

const (
	changeRequestPort uint32 = 0x02
	changeRequestIP   uint32 = 0x04
)

func addChangeRequest(message *stun.Message, changeIP, changePort bool) {
	if !changeIP && !changePort {
		return
	}
	var flags uint32
	if changeIP {
		flags |= changeRequestIP
	}
	if changePort {
		flags |= changeRequestPort
	}
	var value [4]byte
	binary.BigEndian.PutUint32(value[:], flags)
	message.Add(stun.AttrChangeRequest, value[:])
}
