// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package wfp owns transactional Windows filtering objects; it is not itself a complete VPN policy.
package wfp

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	AcceptV4       = windows.GUID{Data1: 0xe1cd9fe7, Data2: 0xf4b5, Data3: 0x4273, Data4: [8]byte{0x96, 0xc0, 0x59, 0x2e, 0x48, 0x7b, 0x86, 0x50}}
	AcceptV6       = windows.GUID{Data1: 0xa3b42c97, Data2: 0x9f04, Data3: 0x4672, Data4: [8]byte{0xb8, 0x7e, 0xce, 0xe9, 0xc4, 0x83, 0x25, 0x7f}}
	ConnectV4      = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82}}
	ConnectV6      = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x44bc, Data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4}}
	TransportV4    = windows.GUID{Data1: 0x09e61aea, Data2: 0xd214, Data3: 0x46e2, Data4: [8]byte{0x9b, 0x21, 0xb2, 0x6b, 0x0b, 0x2f, 0x28, 0xc8}}
	TransportV6    = windows.GUID{Data1: 0xe1735bde, Data2: 0x013f, Data3: 0x4655, Data4: [8]byte{0xb3, 0x51, 0xa4, 0x9e, 0x15, 0x76, 0x2d, 0xf0}}
	remoteAddress  = windows.GUID{Data1: 0xb235ae9a, Data2: 0x1d64, Data3: 0x49b8, Data4: [8]byte{0xa4, 0x4c, 0x5f, 0xf3, 0xd9, 0x09, 0x50, 0x45}}
	remotePort     = windows.GUID{Data1: 0xc35a604d, Data2: 0xd22b, Data3: 0x4e1a, Data4: [8]byte{0x91, 0xb4, 0x68, 0xf6, 0x74, 0xee, 0x67, 0x4b}}
	ipProtocol     = windows.GUID{Data1: 0x3971ef2b, Data2: 0x623e, Data3: 0x4f9a, Data4: [8]byte{0x8c, 0xb1, 0x6e, 0x79, 0xb8, 0x06, 0xb9, 0xa7}}
	localInterface = windows.GUID{Data1: 0x4cd62a49, Data2: 0x59c3, Data3: 0x4969, Data4: [8]byte{0xb7, 0xf3, 0xbd, 0xa5, 0xd3, 0x28, 0x90, 0xa4}}
	applicationID  = windows.GUID{Data1: 0xd78e1e87, Data2: 0x8644, Data3: 0x4ea5, Data4: [8]byte{0x94, 0x37, 0xd8, 0x09, 0xec, 0xef, 0xc9, 0x71}}
	localAddress   = windows.GUID{Data1: 0xd9ee00de, Data2: 0xc1ef, Data3: 0x4617, Data4: [8]byte{0xbf, 0xe3, 0xff, 0xd8, 0xf5, 0xa0, 0x89, 0x57}}
	localPort      = windows.GUID{Data1: 0x0c1ba1af, Data2: 0x5765, Data3: 0x453f, Data4: [8]byte{0xaf, 0x22, 0xa8, 0xf7, 0x91, 0xac, 0x77, 0x5b}}
	conditionFlags = windows.GUID{Data1: 0x632ce23b, Data2: 0x5167, Data3: 0x435c, Data4: [8]byte{0x86, 0xd7, 0xe9, 0x03, 0x68, 0x4a, 0xa8, 0x0c}}
	userID         = windows.GUID{Data1: 0xaf043a0a, Data2: 0xb34d, Data3: 0x4f86, Data4: [8]byte{0x97, 0x9c, 0xc9, 0x03, 0x71, 0xaf, 0x6e, 0x66}}
)

type Action uint32

const (
	Block  Action = 0x1001
	Permit Action = 0x1002
)

// Rule deliberately supports a bounded native subset; missing policy dimensions must not be silently ignored.
type Rule struct {
	Key                windows.GUID
	Layer              windows.GUID
	Priority           uint64
	Action             Action
	Remote             netip.Prefix
	Protocol           uint8
	RemotePort         uint16
	LocalInterface     uint64
	Application        ApplicationID
	User               UserMatch
	LocalAddress       netip.Addr
	LocalPort          uint16
	LoopbackOnly       bool
	ICMP               bool
	ICMPType, ICMPCode uint8
}

// Validate rejects unsupported dimensions before any native policy mutation.
func (r Rule) Validate() error { return r.validate() }

func (r Rule) validate() error {
	if r.Key == (windows.GUID{}) || (r.Action != Block && r.Action != Permit) {
		return fmt.Errorf("invalid WFP rule identity/action")
	}
	is4 := r.Layer == ConnectV4 || r.Layer == TransportV4 || r.Layer == AcceptV4
	if !is4 && r.Layer != ConnectV6 && r.Layer != TransportV6 && r.Layer != AcceptV6 {
		return fmt.Errorf("unsupported WFP layer")
	}
	if r.Remote.IsValid() && (r.Remote != r.Remote.Masked() || r.Remote.Addr().Is4In6() || r.Remote.Addr().Is4() != is4) {
		return fmt.Errorf("invalid WFP remote prefix")
	}
	if (r.RemotePort != 0 || r.LocalPort != 0) && r.Protocol != 6 && r.Protocol != 17 {
		return fmt.Errorf("WFP port condition requires TCP or UDP")
	}
	if r.ICMP {
		if !((is4 && r.Protocol == 1) || (!is4 && r.Protocol == 58)) || r.LocalPort != 0 || r.RemotePort != 0 {
			return fmt.Errorf("invalid WFP ICMP condition")
		}
	} else if r.ICMPType != 0 || r.ICMPCode != 0 {
		return fmt.Errorf("ICMP values require explicit ICMP condition")
	}
	if r.LocalAddress.IsValid() && (r.LocalAddress.Is4In6() || r.LocalAddress.Zone() != "" || r.LocalAddress.Is4() != is4) {
		return fmt.Errorf("invalid WFP local address")
	}
	if !r.Remote.IsValid() && r.Remote != (netip.Prefix{}) {
		return fmt.Errorf("invalid WFP prefix")
	}
	if r.Application != "" {
		if r.Layer != ConnectV4 && r.Layer != ConnectV6 && r.Layer != AcceptV4 && r.Layer != AcceptV6 {
			return fmt.Errorf("application identity is only supported at ALE authorization layers")
		}
		if err := r.Application.validate(); err != nil {
			return err
		}
	}
	if r.User != AnyUser {
		if (r.Layer != ConnectV4 && r.Layer != ConnectV6 && r.Layer != AcceptV4 && r.Layer != AcceptV6) || (r.User != SystemOrAdministrator && r.User != DHCPService) {
			return fmt.Errorf("unsupported WFP user condition or layer")
		}
	}
	return nil
}

type v4Mask struct{ Address, Mask uint32 }
type v6Mask struct {
	Address [16]byte
	Bits    uint8
}
type nativeRule struct {
	Filter     filter
	Conditions []condition
	Priority   uint64
	V4         v4Mask
	V6         v6Mask
	Name       []uint16
	Marker     []byte
	Interface  uint64
	AppBytes   []byte
	AppBlob    blob
	UserBytes  []byte
	UserBlob   blob
	Local6     [16]byte
}

func makeNative(r Rule, c Config) (*nativeRule, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	n := &nativeRule{Priority: r.Priority, Marker: []byte(c.Marker)}
	n.Name, _ = windows.UTF16FromString(c.Name)
	n.Filter = filter{Key: r.Key, Display: display{Name: &n.Name[0]}, Flags: 1, Provider: &c.Provider,
		Data: bytesBlob(n.Marker), Layer: r.Layer, Sublayer: c.Sublayer, Action: action{Type: uint32(r.Action)}}
	n.Filter.Weight = ptrValue(4, unsafe.Pointer(&n.Priority))
	if r.Remote.IsValid() {
		var v value
		if r.Remote.Addr().Is4() {
			a := r.Remote.Addr().As4()
			n.V4 = v4Mask{Address: binary.BigEndian.Uint32(a[:]), Mask: ^uint32(0) << (32 - r.Remote.Bits())}
			v = ptrValue(256, unsafe.Pointer(&n.V4))
		} else {
			n.V6 = v6Mask{Address: r.Remote.Addr().As16(), Bits: uint8(r.Remote.Bits())}
			v = ptrValue(257, unsafe.Pointer(&n.V6))
		}
		n.Conditions = append(n.Conditions, condition{Field: remoteAddress, Value: v})
	}
	if r.Protocol != 0 {
		n.Conditions = append(n.Conditions, condition{Field: ipProtocol, Value: uintValue(1, uint64(r.Protocol))})
	}
	if r.RemotePort != 0 {
		n.Conditions = append(n.Conditions, condition{Field: remotePort, Value: uintValue(2, uint64(r.RemotePort))})
	}
	if r.LocalInterface != 0 {
		n.Interface = r.LocalInterface
		n.Conditions = append(n.Conditions, condition{Field: localInterface, Value: ptrValue(4, unsafe.Pointer(&n.Interface))})
	}
	if r.LocalAddress.IsValid() {
		var v value
		if r.LocalAddress.Is4() {
			a := r.LocalAddress.As4()
			v = uintValue(3, uint64(binary.BigEndian.Uint32(a[:])))
		} else {
			n.Local6 = r.LocalAddress.As16()
			v = ptrValue(11, unsafe.Pointer(&n.Local6))
		}
		n.Conditions = append(n.Conditions, condition{Field: localAddress, Value: v})
	}
	if r.LocalPort != 0 {
		n.Conditions = append(n.Conditions, condition{Field: localPort, Value: uintValue(2, uint64(r.LocalPort))})
	}
	if r.Application != "" {
		n.AppBytes = []byte(r.Application)
		n.AppBlob = bytesBlob(n.AppBytes)
		n.Conditions = append(n.Conditions, condition{Field: applicationID, Value: ptrValue(12, unsafe.Pointer(&n.AppBlob))})
	}
	if r.User != AnyUser {
		var err error
		n.UserBytes, err = r.User.descriptor()
		if err != nil {
			return nil, err
		}
		n.UserBlob = bytesBlob(n.UserBytes)
		n.Conditions = append(n.Conditions, condition{Field: userID, Value: ptrValue(14, unsafe.Pointer(&n.UserBlob))})
	}
	if r.LoopbackOnly {
		n.Conditions = append(n.Conditions, condition{Field: conditionFlags, Match: 6, Value: uintValue(3, 1)})
	}
	if r.ICMP {
		n.Conditions = append(n.Conditions, condition{Field: localPort, Value: uintValue(2, uint64(r.ICMPType))}, condition{Field: remotePort, Value: uintValue(2, uint64(r.ICMPCode))})
	}
	n.Filter.Count = uint32(len(n.Conditions))
	if len(n.Conditions) > 0 {
		n.Filter.Conditions = &n.Conditions[0]
	}
	return n, nil
}

func readRule(f *filter) (Rule, error) {
	var r Rule
	if f == nil {
		return r, fmt.Errorf("nil native WFP rule")
	}
	if f.Count > 9 || (f.Count > 0 && f.Conditions == nil) || f.Flags&1 == 0 || f.Flags&^uint32(0x61) != 0 ||
		f.Context[0] != 0 || f.Weight.Type != 4 || ptrFromValue[uint64](f.Weight) == nil {
		return r, fmt.Errorf("unsupported native WFP rule state: flags=%#x count=%d rawContext=%#x weightType=%d weightPresent=%t", f.Flags, f.Count, f.Context[0], f.Weight.Type, ptrFromValue[uint64](f.Weight) != nil)
	}
	r = Rule{Key: f.Key, Layer: f.Layer, Priority: *ptrFromValue[uint64](f.Weight), Action: Action(f.Action.Type)}
	seen := make(map[windows.GUID]bool)
	for _, c := range unsafe.Slice(f.Conditions, int(f.Count)) {
		if (c.Match != 0 && !(c.Field == conditionFlags && c.Match == 6)) || seen[c.Field] {
			return Rule{}, fmt.Errorf("unsupported or duplicated WFP condition")
		}
		seen[c.Field] = true
		switch c.Field {
		case userID:
			if c.Value.Type != 14 {
				return Rule{}, fmt.Errorf("invalid WFP user value type")
			}
			p := ptrFromValue[blob](c.Value)
			if p == nil {
				return Rule{}, fmt.Errorf("nil WFP user descriptor")
			}
			b, err := copyBlob(*p)
			if err != nil {
				return Rule{}, err
			}
			r.User, err = readUserDescriptor(b)
			if err != nil {
				return Rule{}, err
			}
		case conditionFlags:
			if c.Match != 6 || c.Value.Type != 3 || binary.LittleEndian.Uint32(c.Value.Data[:4]) != 1 {
				return Rule{}, fmt.Errorf("unsupported WFP flags condition")
			}
			r.LoopbackOnly = true
		case localAddress:
			switch c.Value.Type {
			case 3:
				var a [4]byte
				binary.BigEndian.PutUint32(a[:], binary.LittleEndian.Uint32(c.Value.Data[:4]))
				r.LocalAddress = netip.AddrFrom4(a)
			case 11:
				p := ptrFromValue[[16]byte](c.Value)
				if p == nil {
					return Rule{}, fmt.Errorf("nil WFP local IPv6 address")
				}
				r.LocalAddress = netip.AddrFrom16(*p)
			default:
				return Rule{}, fmt.Errorf("unsupported WFP local address condition")
			}
		case localPort:
			if c.Value.Type != 2 {
				return Rule{}, fmt.Errorf("invalid WFP local port")
			}
			r.LocalPort = binary.LittleEndian.Uint16(c.Value.Data[:2])
		case localInterface:
			if c.Value.Type != 4 {
				return Rule{}, fmt.Errorf("invalid WFP interface value type")
			}
			p := ptrFromValue[uint64](c.Value)
			if p == nil || *p == 0 {
				return Rule{}, fmt.Errorf("invalid WFP interface LUID")
			}
			r.LocalInterface = *p
		case applicationID:
			if c.Value.Type != 12 {
				return Rule{}, fmt.Errorf("invalid WFP application value type")
			}
			p := ptrFromValue[blob](c.Value)
			if p == nil {
				return Rule{}, fmt.Errorf("invalid WFP application identity")
			}
			b, err := copyBlob(*p)
			if err != nil {
				return Rule{}, err
			}
			r.Application = ApplicationID(b)
			if r.Application == "" {
				return Rule{}, fmt.Errorf("empty native application condition")
			}
		case remoteAddress:
			switch c.Value.Type {
			case 256:
				p := ptrFromValue[v4Mask](c.Value)
				if p == nil {
					return Rule{}, fmt.Errorf("nil WFP IPv4 mask")
				}
				var a [4]byte
				binary.BigEndian.PutUint32(a[:], p.Address)
				bits := 0
				for bits < 32 && p.Mask&(1<<uint(31-bits)) != 0 {
					bits++
				}
				if p.Mask != ^uint32(0)<<(32-bits) {
					return Rule{}, fmt.Errorf("noncontiguous WFP mask")
				}
				r.Remote = netip.PrefixFrom(netip.AddrFrom4(a), bits)
			case 257:
				p := ptrFromValue[v6Mask](c.Value)
				if p == nil || p.Bits > 128 {
					return Rule{}, fmt.Errorf("invalid WFP IPv6 mask")
				}
				r.Remote = netip.PrefixFrom(netip.AddrFrom16(p.Address), int(p.Bits))
			default:
				return Rule{}, fmt.Errorf("unsupported WFP address condition")
			}
		case ipProtocol:
			if c.Value.Type != 1 || c.Value.Data[0] == 0 {
				return Rule{}, fmt.Errorf("invalid WFP protocol type")
			}
			r.Protocol = c.Value.Data[0]
		case remotePort:
			if c.Value.Type != 2 {
				return Rule{}, fmt.Errorf("invalid WFP port type")
			}
			r.RemotePort = binary.LittleEndian.Uint16(c.Value.Data[:2])
		default:
			return Rule{}, fmt.Errorf("unsupported WFP condition field")
		}
	}
	if (seen[localPort] || seen[remotePort]) && (r.Protocol == 1 || r.Protocol == 58) {
		if !seen[localPort] || !seen[remotePort] || r.LocalPort > 255 || r.RemotePort > 255 {
			return Rule{}, fmt.Errorf("incomplete WFP ICMP type/code")
		}
		r.ICMP = true
		r.ICMPType = uint8(r.LocalPort)
		r.ICMPCode = uint8(r.RemotePort)
		r.LocalPort = 0
		r.RemotePort = 0
	} else if (seen[localPort] && r.LocalPort == 0) || (seen[remotePort] && r.RemotePort == 0) {
		return Rule{}, fmt.Errorf("explicit zero port is not a wildcard")
	}
	return r, r.validate()
}
