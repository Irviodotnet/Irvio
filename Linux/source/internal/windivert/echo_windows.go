// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windivert

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/netip"

	"github.com/Irviodotnet/Irvio/internal/winroute"
)

type echoCapture struct {
	remote               netip.Addr
	identifier, sequence uint16
	nonce                [16]byte
}

// OpenEchoReplyCapture requires a fresh secret nonce in the first 16 payload bytes, not a reserved echo ID.
func (m *Module) OpenEchoReplyCapture(b winroute.Binding, remote netip.Addr, identifier, sequence uint16, nonce [16]byte) (*Handle, error) {
	if !remote.Is4() || !remote.IsGlobalUnicast() || remote.IsLoopback() || remote == b.Source || nonce == ([16]byte{}) {
		return nil, ErrPacket
	}
	if err := validBinding(b); err != nil {
		return nil, err
	}
	spec := &echoCapture{remote: remote, identifier: identifier, sequence: sequence, nonce: nonce}
	h, err := m.openIO(spec.filter(b), 0, 4)
	if h != nil {
		h.binding, h.echo = b, spec
	}
	return h, err
}

func (e *echoCapture) filter(b winroute.Binding) string {
	f := fmt.Sprintf("inbound and ip and icmp and !loopback and ifIdx==%d and ip.HdrLength==5 and ip.FragOff==0 and !ip.MF and ip.Length>=44 and ip.SrcAddr==%s and ip.DstAddr==%s and icmp.Type==0 and icmp.Code==0 and packet16[24b]==%d and packet16[26b]==%d", b.Index, e.remote, b.Source, e.identifier, e.sequence)
	for i := 0; i < len(e.nonce); i += 4 {
		f += fmt.Sprintf(" and packet32[%db]==%d", 28+i, binary.BigEndian.Uint32(e.nonce[i:i+4]))
	}
	return f
}

func (e *echoCapture) matches(b []byte) bool {
	return len(b) >= 44 && b[9] == 1 && b[20] == 0 && b[21] == 0 && binary.BigEndian.Uint16(b[6:8])&0xbfff == 0 &&
		netip.AddrFrom4([4]byte(b[12:16])) == e.remote && binary.BigEndian.Uint16(b[24:26]) == e.identifier &&
		binary.BigEndian.Uint16(b[26:28]) == e.sequence && bytes.Equal(b[28:44], e.nonce[:])
}
