// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productintent

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// scmSession identifies the OS service-manager instance, not an estimated wall-clock boot time.
type scmSession struct {
	PID     uint32 `json:"pid"`
	Created uint64 `json:"created"`
}

func (s scmSession) valid() bool { return s.PID != 0 && s.Created != 0 }

var errNotSCM = errors.New("not the Windows service-manager executable")

func currentSCMSession() (scmSession, error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return scmSession{}, err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return scmSession{}, err
	}
	defer windows.CloseHandle(snapshot)
	var result scmSession
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "services.exe") {
			continue
		}
		candidate, queryErr := inspectSCM(entry.ProcessID, filepath.Join(systemDir, "services.exe"))
		if errors.Is(queryErr, errNotSCM) {
			continue
		}
		if queryErr != nil {
			return scmSession{}, fmt.Errorf("verify Windows service-manager session: %w", queryErr)
		}
		if result.valid() {
			return scmSession{}, errors.New("ambiguous Windows service-manager session")
		}
		result = candidate
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return scmSession{}, err
	}
	if !result.valid() {
		return scmSession{}, errors.New("Windows service-manager session unavailable")
	}
	return result, nil
}

func inspectSCM(pid uint32, expectedImage string) (scmSession, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return scmSession{}, err
	}
	defer windows.CloseHandle(handle)
	var image [32768]uint16
	length := uint32(len(image))
	if err := windows.QueryFullProcessImageName(handle, 0, &image[0], &length); err != nil {
		return scmSession{}, err
	}
	if !strings.EqualFold(windows.UTF16ToString(image[:length]), expectedImage) {
		return scmSession{}, errNotSCM
	}
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return scmSession{}, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return scmSession{}, err
	}
	var session, size uint32
	if err := windows.GetTokenInformation(token, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), uint32(unsafe.Sizeof(session)), &size); err != nil {
		return scmSession{}, err
	}
	if user.User.Sid.String() != "S-1-5-18" || session != 0 || size != 4 {
		return scmSession{}, errors.New("untrusted service-manager identity")
	}
	var created, exited, kernel, userTime windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &userTime); err != nil {
		return scmSession{}, err
	}
	result := scmSession{PID: pid, Created: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)}
	if !result.valid() {
		return scmSession{}, errors.New("invalid service-manager process identity")
	}
	return result, nil
}
