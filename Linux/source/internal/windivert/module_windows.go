// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package windivert owns the pinned Windows Exit driver boundary, not network policy.
package windivert

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/winthread"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	dllHash = "c1e060ee19444a259b2162f8af0f3fe8c4428a1c6f694dce20de194ac8d7d9a2"
	sysHash = "8da085332782708d8767bcace5327a6ec7283c17cfb85e40b03cd2323a90ddc2"
)

var ErrPayload = errors.New("Windows Exit driver payload is missing, unsafe or untrusted")
var ErrService = errors.New("Windows Exit driver service must be installed and running from the verified package path")

type payload struct {
	dir   *winfile.Directory
	files []*os.File
	path  string
}

func (p *payload) close() error {
	var result error
	for _, f := range p.files {
		result = errors.Join(result, f.Close())
	}
	return errors.Join(result, p.dir.Close())
}

// PackageDirectory is a dedicated installer-owned directory, never a working-directory or PATH search.
func PackageDirectory() (string, error) {
	if runtime.GOARCH != "amd64" {
		return "", ErrPayload
	}
	root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "IRVIO Exit Driver"), nil
}

func verifyPayload(path string) (*payload, error) {
	d, err := winfile.OpenSystemDirectory(path)
	if err != nil {
		return nil, errors.Join(ErrPayload, err)
	}
	p := &payload{dir: d, path: path}
	fail := func(cause error) (*payload, error) { return nil, errors.Join(ErrPayload, cause, p.close()) }
	for _, file := range []struct{ name, hash string }{{"WinDivert.dll", dllHash}, {"WinDivert64.sys", sysHash}} {
		f, err := d.OpenImmutableFile(file.name)
		if err != nil {
			return fail(err)
		}
		p.files = append(p.files, f)
		info, err := f.Stat()
		if err != nil {
			return fail(err)
		}
		if info.Size() <= 0 || info.Size() > 4<<20 {
			return fail(fmt.Errorf("invalid %s size", file.name))
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return fail(err)
		}
		if hex.EncodeToString(h.Sum(nil)) != file.hash {
			return fail(fmt.Errorf("%s hash mismatch", file.name))
		}
	}
	if err := verifyDriverSignature(p.files[1]); err != nil {
		return fail(err)
	}
	return p, nil
}

func verifyDriverSignature(file *os.File) error {
	path, err := windows.UTF16PtrFromString(file.Name())
	if err != nil {
		return err
	}
	info := windows.WinTrustFileInfo{FilePath: path, File: windows.Handle(file.Fd())}
	info.Size = uint32(unsafe.Sizeof(info))
	data := windows.WinTrustData{
		UIChoice: windows.WTD_UI_NONE, RevocationChecks: windows.WTD_REVOKE_WHOLECHAIN,
		UnionChoice: windows.WTD_CHOICE_FILE, FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&info),
		StateAction: windows.WTD_STATEACTION_VERIFY,
		ProvFlags:   windows.WTD_CACHE_ONLY_URL_RETRIEVAL | windows.WTD_REVOCATION_CHECK_CHAIN_EXCLUDE_ROOT | windows.WTD_DISABLE_MD2_MD4,
	}
	data.Size = uint32(unsafe.Sizeof(data))
	err = windows.WinVerifyTrustEx(0, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	cleanup := windows.WinVerifyTrustEx(0, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	runtime.KeepAlive(info)
	if err != nil || cleanup != nil {
		return fmt.Errorf("verify Exit driver signature (cached trust): %w", errors.Join(err, cleanup))
	}
	return nil
}

// Module and its immutable payload remain pinned until process exit because the vendor DLL owns thread TLS.
type Module struct {
	payload                           *payload
	dll                               *windows.DLL
	open, close, send, recv, setParam *windows.Proc
	probeMu                           sync.Mutex
	failedProbe                       windows.Handle
}

var moduleState struct {
	sync.Mutex
	module *Module
}

func Load() (*Module, error) {
	moduleState.Lock()
	defer moduleState.Unlock()
	if moduleState.module != nil {
		return moduleState.module, nil
	}
	path, err := PackageDirectory()
	if err != nil {
		return nil, err
	}
	p, err := verifyPayload(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Join(path, "WinDivert.dll")
	h, err := windows.LoadLibraryEx(name, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, errors.Join(err, p.close())
	}
	m := &Module{payload: p, dll: &windows.DLL{Name: name, Handle: h}}
	for _, item := range []struct {
		name   string
		target **windows.Proc
	}{
		{"WinDivertOpen", &m.open}, {"WinDivertClose", &m.close},
		{"WinDivertSendEx", &m.send}, {"WinDivertRecvEx", &m.recv},
		{"WinDivertSetParam", &m.setParam},
	} {
		*item.target, err = m.dll.FindProc(item.name)
		if err != nil {
			return nil, errors.Join(err, m.dll.Release(), p.close())
		}
	}
	moduleState.module = m
	return m, nil
}

func (m *Module) checkService() error {
	if m == nil || m.payload == nil {
		return ErrService
	}
	manager, err := mgr.Connect()
	if err != nil {
		return errors.Join(ErrService, err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService("WinDivert")
	if err != nil {
		return errors.Join(ErrService, err)
	}
	defer service.Close()
	c, err := service.Config()
	if err != nil {
		return errors.Join(ErrService, err)
	}
	path := c.BinaryPathName
	if strings.HasPrefix(path, `\??\`) {
		path = strings.TrimPrefix(path, `\??\`)
	}
	if strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`) {
		path = path[1 : len(path)-1]
	}
	if c.ServiceType != windows.SERVICE_KERNEL_DRIVER || c.StartType != windows.SERVICE_DEMAND_START || !strings.EqualFold(path, filepath.Join(m.payload.path, "WinDivert64.sys")) {
		return ErrService
	}
	s, err := service.Query()
	if err != nil || s.State != svc.Running {
		return errors.Join(ErrService, err)
	}
	return nil
}

// Probe checks driver compatibility without capturing traffic, installing a service or sending packets.
func (m *Module) Probe() error {
	if m == nil {
		return ErrService
	}
	m.probeMu.Lock()
	defer m.probeMu.Unlock()
	if m.failedProbe != 0 {
		ok, _, err := m.close.Call(uintptr(m.failedProbe))
		if ok == 0 {
			return fmt.Errorf("retry Exit probe close: %w", err)
		}
		m.failedProbe = 0
	}
	if err := m.checkService(); err != nil {
		return err
	}
	_, err := winthread.Call(func() (struct{}, error) {
		filter := []byte("false\x00")
		h, _, err := m.open.Call(uintptr(unsafe.Pointer(&filter[0])), 1, 0, 8|16)
		runtime.KeepAlive(filter)
		if windows.Handle(h) == windows.InvalidHandle {
			return struct{}{}, err
		}
		ok, _, err := m.close.Call(h)
		if ok == 0 {
			m.failedProbe = windows.Handle(h)
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}
