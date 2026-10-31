// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"fmt"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ole = windows.NewLazySystemDLL("ole32.dll")
var automation = windows.NewLazySystemDLL("oleaut32.dll")
var createInstance = ole.NewProc("CoCreateInstance")
var allocString = automation.NewProc("SysAllocStringLen")
var freeString = automation.NewProc("SysFreeString")
var stringLength = automation.NewProc("SysStringLen")
var variantClear = automation.NewProc("VariantClear")
var arrayCreate = automation.NewProc("SafeArrayCreateVector")
var arrayPut = automation.NewProc("SafeArrayPutElement")
var arrayGet = automation.NewProc("SafeArrayGetElement")
var arrayBound = automation.NewProc("SafeArrayGetUBound")
var arrayLower = automation.NewProc("SafeArrayGetLBound")
var arrayDimension = automation.NewProc("SafeArrayGetDim")

var classPolicy = windows.GUID{Data1: 0xe2b3c97f, Data2: 0x6ae1, Data3: 0x41ac, Data4: [8]byte{0x81, 0x7a, 0xf6, 0xf9, 0x21, 0x66, 0xd7, 0xdd}}
var iidPolicy = windows.GUID{Data1: 0x98325047, Data2: 0xc671, Data3: 0x4174, Data4: [8]byte{0x8d, 0x81, 0xde, 0xfc, 0xd3, 0xf0, 0x31, 0x86}}
var classRule = windows.GUID{Data1: 0x2c5bc43e, Data2: 0x3369, Data3: 0x4c33, Data4: [8]byte{0xab, 0x0c, 0xbe, 0x94, 0x69, 0x67, 0x7a, 0xf4}}
var iidRule = windows.GUID{Data1: 0xaf230d27, Data2: 0xbaba, Data3: 0x4e42, Data4: [8]byte{0xac, 0xed, 0xf5, 0x24, 0xf2, 0x2c, 0xfc, 0xe2}}

type result uint32

func (r result) Error() string { return fmt.Sprintf("Windows Firewall HRESULT 0x%08x", uint32(r)) }
func checked(r uintptr) error {
	if int32(r) < 0 {
		return result(r)
	}
	return nil
}

type object struct{ table *[43]uintptr }
type variant struct {
	kind         uint16
	reserved     [3]uint16
	value, extra uintptr
}

// The Windows x64 ABI requires 16-byte alignment for caller-owned by-value aggregates.
func alignedVariant() *variant {
	storage := new([48]byte)
	offset := -uintptr(unsafe.Pointer(&storage[0])) & 15
	return (*variant)(unsafe.Pointer(&storage[offset]))
}

//go:uintptrescapes
func (o *object) call(index int, args ...uintptr) error {
	values := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	r, _, _ := syscall.SyscallN(o.table[index], values...)
	runtime.KeepAlive(o)
	return checked(r)
}
func (o *object) release() {
	if o != nil {
		o.call(2)
	}
}
func create(class, iid *windows.GUID) (*object, error) {
	var out *object
	r, _, _ := createInstance.Call(uintptr(unsafe.Pointer(class)), 0, 1, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	return out, checked(r)
}
func newString(s string) (uintptr, error) {
	v := utf16.Encode([]rune(s))
	v = append(v, 0)
	r, _, _ := allocString.Call(uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)-1))
	runtime.KeepAlive(v)
	if r == 0 {
		return 0, fmt.Errorf("allocate Firewall BSTR")
	}
	return r, nil
}
func readString(p uintptr) (string, error) {
	if p == 0 {
		return "", nil
	}
	n, _, _ := stringLength.Call(p)
	if n > 32768 {
		return "", fmt.Errorf("oversized Firewall BSTR")
	}
	pointer := *(**uint16)(unsafe.Pointer(&p))
	return string(utf16.Decode(unsafe.Slice(pointer, int(n)))), nil
}
func (o *object) putString(index int, s string) error {
	b, err := newString(s)
	if err != nil {
		return err
	}
	defer freeString.Call(b)
	return o.call(index, b)
}
func (o *object) getString(index int) (string, error) {
	var p uintptr
	if err := o.call(index, uintptr(unsafe.Pointer(&p))); err != nil {
		return "", err
	}
	defer freeString.Call(p)
	return readString(p)
}
func (o *object) getNumber(index int) (uint32, error) {
	var n uint32
	err := o.call(index, uintptr(unsafe.Pointer(&n)))
	return n, err
}

func (o *object) putInterface(name string) error {
	b, err := newString(name)
	if err != nil {
		return err
	}
	defer freeString.Call(b)
	a, _, _ := arrayCreate.Call(12, 0, 1)
	if a == 0 {
		return fmt.Errorf("allocate Firewall interface array")
	}
	v := alignedVariant()
	*v = variant{kind: 0x200c, value: a}
	defer variantClear.Call(uintptr(unsafe.Pointer(v)))
	element := variant{kind: 8, value: b}
	var index int32
	hr, _, _ := arrayPut.Call(a, uintptr(unsafe.Pointer(&index)), uintptr(unsafe.Pointer(&element)))
	if err := checked(hr); err != nil {
		return err
	}
	return o.call(30, uintptr(unsafe.Pointer(v)))
}
func (o *object) getInterface() (string, error) {
	var v variant
	if err := o.call(29, uintptr(unsafe.Pointer(&v))); err != nil {
		return "", err
	}
	defer variantClear.Call(uintptr(unsafe.Pointer(&v)))
	if v.kind != 0x200c || v.value == 0 {
		return "", fmt.Errorf("unexpected Firewall interface array")
	}
	dim, _, _ := arrayDimension.Call(v.value)
	if dim != 1 {
		return "", fmt.Errorf("unexpected Firewall interface dimensions")
	}
	var low, high int32
	hr, _, _ := arrayLower.Call(v.value, 1, uintptr(unsafe.Pointer(&low)))
	if err := checked(hr); err != nil {
		return "", err
	}
	hr, _, _ = arrayBound.Call(v.value, 1, uintptr(unsafe.Pointer(&high)))
	if err := checked(hr); err != nil {
		return "", err
	}
	if low != 0 || high != 0 {
		return "", fmt.Errorf("unexpected Firewall interface count")
	}
	var element variant
	hr, _, _ = arrayGet.Call(v.value, uintptr(unsafe.Pointer(&low)), uintptr(unsafe.Pointer(&element)))
	if err := checked(hr); err != nil {
		return "", err
	}
	defer variantClear.Call(uintptr(unsafe.Pointer(&element)))
	if element.kind != 8 {
		return "", fmt.Errorf("unexpected Firewall interface type")
	}
	return readString(element.value)
}
