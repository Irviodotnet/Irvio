// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package wfp

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

type display struct{ Name, Description *uint16 }
type blob struct {
	Size uint32
	Data *byte
}
type value struct {
	_    [0]uint64
	Type uint32
	_    uint32
	Data [8]byte
}
type condition struct {
	Field windows.GUID
	Match uint32
	Value value
}
type action struct {
	Type uint32
	Key  windows.GUID
}
type provider struct {
	Key     windows.GUID
	Display display
	Flags   uint32
	Data    blob
	Service *uint16
}
type sublayer struct {
	Key      windows.GUID
	Display  display
	Flags    uint32
	Provider *windows.GUID
	Data     blob
	Weight   uint16
}
type filter struct {
	Key             windows.GUID
	Display         display
	Flags           uint32
	Provider        *windows.GUID
	Data            blob
	Layer, Sublayer windows.GUID
	Weight          value
	Count           uint32
	Conditions      *condition
	Action          action
	Context         [2]uint64
	Reserved        *windows.GUID
	ID              uint64
	EffectiveWeight value
}
type enumTemplate struct {
	Provider        *windows.GUID
	Layer           windows.GUID
	Type, Flags     uint32
	ProviderContext unsafe.Pointer
	Count           uint32
	Conditions      *condition
	ActionMask      uint32
	Callout         *windows.GUID
}
type layer struct {
	Key             windows.GUID
	Display         display
	Flags, Count    uint32
	Fields          unsafe.Pointer
	DefaultSublayer windows.GUID
	ID              uint16
}
type session struct {
	Key                windows.GUID
	Display            display
	Flags, WaitMS, PID uint32
	SID                *windows.SID
	User               *uint16
	KernelMode         int32
}

var fwpuclnt = windows.NewLazySystemDLL("fwpuclnt.dll")

//go:uintptrescapes
func call(name string, args ...uintptr) error {
	r, _, _ := fwpuclnt.NewProc(name).Call(args...)
	if r != 0 {
		return fmt.Errorf("%s: %w", name, windows.Errno(r))
	}
	return nil
}

func release[T any](p *T) {
	if p != nil {
		fwpuclnt.NewProc("FwpmFreeMemory0").Call(uintptr(unsafe.Pointer(&p)))
	}
}

func bytesBlob(b []byte) blob {
	if len(b) == 0 {
		return blob{}
	}
	return blob{Size: uint32(len(b)), Data: &b[0]}
}

func copyBlob(b blob) ([]byte, error) {
	if b.Size > 65536 || (b.Size != 0 && b.Data == nil) {
		return nil, fmt.Errorf("invalid native WFP blob")
	}
	return append([]byte(nil), unsafe.Slice(b.Data, int(b.Size))...), nil
}

func uintValue(typ uint32, n uint64) value {
	v := value{Type: typ}
	*(*uint64)(unsafe.Pointer(&v.Data[0])) = n
	return v
}

// Pointer-bearing union values retain their referents in the enclosing nativeRule until the syscall returns.
func ptrValue(typ uint32, p unsafe.Pointer) value {
	v := value{Type: typ}
	*(*unsafe.Pointer)(unsafe.Pointer(&v.Data[0])) = p
	return v
}

func ptrFromValue[T any](v value) *T { return (*T)(*(*unsafe.Pointer)(unsafe.Pointer(&v.Data[0]))) }

func openEngine() (windows.Handle, error) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return 0, fmt.Errorf("WFP backend requires 64-bit Windows")
	}
	s := session{WaitMS: 5000}
	var h windows.Handle
	err := call("FwpmEngineOpen0", 0, 10, 0, uintptr(unsafe.Pointer(&s)), uintptr(unsafe.Pointer(&h)))
	runtime.KeepAlive(&s)
	return h, err
}
