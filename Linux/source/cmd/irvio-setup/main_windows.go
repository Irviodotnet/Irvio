// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows"
)

var desktopSID = regexp.MustCompile(`^S-1-5-21-[0-9]+-[0-9]+-[0-9]+-[0-9]+$`)

func main() {
	quiet := developmentUnattended(os.Args[1:])
	if err := runSetup(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "IRVIO setup:", err)
		if !quiet && !(len(os.Args) == 2 && os.Args[1] == "--verify-bundle") {
			_, _ = dialog("IRVIO setup did not complete.\n\n"+err.Error(), windows.MB_OK|windows.MB_ICONERROR)
		}
		os.Exit(1)
	}
}

func dialog(message string, style uint32) (int32, error) {
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return 0, err
	}
	caption, _ := windows.UTF16PtrFromString("IRVIO Setup")
	return windows.MessageBox(0, text, caption, style)
}

func setupSID(args []string, elevated bool, ownSID string) (string, bool, error) {
	if len(args) == 0 && desktopSID.MatchString(ownSID) {
		return ownSID, false, nil
	}
	if elevated && (len(args) == 2 || len(args) == 3 && development == "true" && args[2] == "--development-unattended") && args[0] == "--internal-install-for" && desktopSID.MatchString(args[1]) {
		return args[1], true, nil
	}
	return "", false, errors.New("launch IRVIO Setup as the intended desktop user; unsupported installer arguments")
}

type setupIntent struct {
	SID                             string
	Child, Uninstall, RemoveProfile bool
	Server                          bool
	Bootstrap                       string
}

func developmentUnattended(args []string) bool {
	return development == "true" && (len(args) == 3 || len(args) == 4) && args[len(args)-1] == "--development-unattended"
}

func parseSetupIntent(args []string, elevated bool, ownSID string) (setupIntent, error) {
	if len(args) == 3 && args[0] == "--role" && args[1] == "server" && args[2] == "--uninstall" {
		return setupIntent{Server: true, Uninstall: true}, nil
	}
	if elevated && (len(args) == 2 || len(args) == 3 && developmentUnattended(args)) && args[0] == "--internal-uninstall-server" && (args[1] == "keep-profile" || args[1] == "remove-profile") {
		return setupIntent{Server: true, Child: true, Uninstall: true, RemoveProfile: args[1] == "remove-profile"}, nil
	}
	if len(args) == 4 && args[0] == "--role" && args[1] == "server" && args[2] == "--bootstrap" {
		return serverSetupIntent(args[3], false)
	}
	if elevated && (len(args) == 2 || len(args) == 3 && developmentUnattended(args)) && args[0] == "--internal-install-server" {
		return serverSetupIntent(args[1], true)
	}
	if len(args) == 1 && args[0] == "--uninstall" && desktopSID.MatchString(ownSID) {
		return setupIntent{SID: ownSID, Uninstall: true}, nil
	}
	if elevated && (len(args) == 3 || len(args) == 4 && developmentUnattended(args)) && args[0] == "--internal-uninstall-for" && desktopSID.MatchString(args[1]) && (args[2] == "keep-profile" || args[2] == "remove-profile") {
		return setupIntent{SID: args[1], Child: true, Uninstall: true, RemoveProfile: args[2] == "remove-profile"}, nil
	}
	sid, child, err := setupSID(args, elevated, ownSID)
	return setupIntent{SID: sid, Child: child}, err
}

func serverSetupIntent(code string, child bool) (setupIntent, error) {
	bundle, err := productbootstrap.Decode(code)
	if err != nil {
		return setupIntent{}, fmt.Errorf("invalid server bootstrap bundle: %w", err)
	}
	addresses, err := productbootstrap.Multiaddrs(bundle)
	if err != nil || len(productruntime.BootstrapSTUNIPv4(addresses)) == 0 {
		return setupIntent{}, errors.New("server bootstrap requires at least one eligible public IPv4 entry")
	}
	canonical, err := productbootstrap.Encode(bundle)
	return setupIntent{Server: true, Child: child, Bootstrap: canonical}, err
}

func (intent setupIntent) elevationArguments() string {
	if intent.Uninstall {
		choice := "keep-profile"
		if intent.RemoveProfile {
			choice = "remove-profile"
		}
		if intent.Server {
			return "--internal-uninstall-server " + choice
		}
		return "--internal-uninstall-for " + intent.SID + " " + choice
	}
	if intent.Server {
		return "--internal-install-server " + intent.Bootstrap
	}
	return "--internal-install-for " + intent.SID
}

func runSetup(args []string) error {
	zr, err := loadBundle()
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "--verify-bundle" {
		fmt.Printf("IRVIO installer bundle: files=%d manifest_sha256=%s development=%s\n", len(zr.File), manifestSHA, development)
		return nil
	}
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	intent, err := parseSetupIntent(args, token.IsElevated(), user.User.Sid.String())
	if err != nil {
		return err
	}
	if !intent.Child {
		account := ""
		if !intent.Server {
			identity, err := windows.StringToSid(intent.SID)
			if err != nil {
				return err
			}
			name, domain, _, err := identity.LookupAccount("")
			if err != nil {
				return err
			}
			account = domain + `\` + name
		}
		label := "Install or update IRVIO for " + account + "?\n\nClose the IRVIO UI before updating. Administrator permission will be requested. Your existing profile will be retained. No VPN connection will be started."
		if intent.Server {
			label = "Install or update an IRVIO server on this computer?\n\nNo UI will be installed. Identity and the existing bootstrap are retained on update. Automatic-start servers start now and at boot; existing manual/disabled modes are preserved. The server provides discovery and may forward other participants' traffic through your Internet connection. Administrator permission will be requested."
		}
		if intent.Uninstall {
			question := "Also delete your IRVIO connection profile?\n\nYes: erase identity keys, bootstrap and Private VPN access. This cannot be undone.\nNo: keep the profile for reinstallation (recommended).\nCancel: do not uninstall.\n\nConfiguration, diagnostics and UI preferences are not erased."
			if intent.Server {
				question = "Also delete this IRVIO server's profile?\n\nYes: erase server identity keys and stored peer/private access state. Reinstallation creates a new identity.\nNo: keep the profile for reinstallation (recommended).\nCancel: do not uninstall.\n\nThe deployment bootstrap configuration and diagnostic history are retained."
			}
			answer, err := dialog(question, windows.MB_YESNOCANCEL|windows.MB_ICONWARNING|windows.MB_DEFBUTTON2)
			if err != nil || answer != 6 && answer != 7 {
				return err
			}
			intent.RemoveProfile = answer == 6
			choice := "Your connection profile will be kept."
			if intent.RemoveProfile {
				choice = "Your connection profile and identity keys will be erased permanently."
			}
			label = "Uninstall IRVIO for " + account + "?\n\n" + choice + "\nClose the IRVIO UI first. Administrator permission will be requested. Removal proceeds only after safe network cleanup."
			if intent.Server {
				label = "Uninstall this IRVIO server?\n\n" + choice + "\nServer operation will stop, including downstream Exit sessions. Administrator permission will be requested. Removal proceeds only after safe network cleanup."
			}
		}
		if development == "true" {
			label = "DEVELOPMENT BUILD — not a signed release.\n\n" + label
		}
		answer, err := dialog(label, windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_DEFBUTTON2)
		if err != nil || answer != 6 {
			return err
		}
		if !token.IsElevated() {
			return elevate(intent)
		}
	}
	common, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	stage := filepath.Join(common, "IRVIO-Setup-"+hex.EncodeToString(nonce[:]))
	dir, err := winfile.OpenDirectory(stage, true)
	if err != nil {
		return fmt.Errorf("create protected installer staging: %w", err)
	}
	defer dir.Close()
	if err := extractBundle(zr, stage); err != nil {
		return fmt.Errorf("extract verified installer; retained at %s: %w", stage, err)
	}
	log, err := dir.OpenFile("install.log", true, true)
	if err != nil {
		return err
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return errors.Join(err, log.Close())
	}
	backend := "install-or-update.ps1"
	if intent.Uninstall {
		backend = "uninstall-for-user.ps1"
		if intent.Server {
			backend = "uninstall-desktop.ps1"
		}
	} else if intent.Server {
		backend = "install-or-update-server.ps1"
	}
	parameters := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(stage, "scripts", backend),
		"-Payload", filepath.Join(stage, "payload"), "-ManifestSHA256", manifestSHA}
	if intent.Server {
		if intent.Uninstall {
			parameters = append(parameters, "-Role", "server")
		} else {
			parameters = append(parameters, "-Bootstrap", intent.Bootstrap)
		}
	} else {
		parameters = append(parameters, "-DesktopSID", intent.SID)
	}
	if intent.RemoveProfile {
		parameters = append(parameters, "-RemoveProfile")
	}
	if development == "true" {
		parameters = append(parameters, "-DevelopmentUnsigned")
	}
	command := exec.Command(filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), parameters...)
	command.Dir = system
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Stdout, command.Stderr = log, log
	runErr := command.Run()
	closeErr := errors.Join(log.Sync(), log.Close())
	if err := errors.Join(runErr, closeErr); err != nil {
		return fmt.Errorf("setup operation failed; protected diagnostic log: %s: %w", filepath.Join(stage, "install.log"), err)
	}
	if err := dir.Close(); err != nil {
		return err
	}
	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("operation completed, but installer staging cleanup failed at %s: %w", stage, err)
	}
	if !developmentUnattended(args) {
		message := "IRVIO installation/update completed. Open IRVIO from the Start menu.\n\nNo VPN connection has been started."
		if intent.Server {
			message = "IRVIO server package installed or updated. No UI was installed. Automatic-start service is running; existing manual/disabled startup modes are preserved.\n\nExit readiness still depends on M11 quorum, GeoIP and network reachability. A running service alone does not mean a selectable Exit."
		}
		if intent.Uninstall {
			message = "IRVIO application and services removed. Your connection profile was kept."
			if intent.RemoveProfile {
				message = "IRVIO application and services removed. Your connection profile was erased. Configuration, diagnostic history and UI preferences were retained."
			}
			if intent.Server {
				message = "IRVIO server application and services removed. The server profile was kept for reinstallation. Deployment bootstrap configuration and diagnostic history were retained."
				if intent.RemoveProfile {
					message = "IRVIO server application and services removed. The server profile was erased; reinstallation will create a new identity. Deployment bootstrap configuration and diagnostic history were retained."
				}
			}
		}
		_, err = dialog(message, windows.MB_OK|windows.MB_ICONINFORMATION)
	}
	return err
}

func elevate(intent setupIntent) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	params, _ := windows.UTF16PtrFromString(intent.elevationArguments())
	directory, _ := windows.UTF16PtrFromString(system)
	type shellExecuteInfo struct {
		Size, Mask                        uint32
		Window                            windows.Handle
		Verb, File, Parameters, Directory *uint16
		Show                              int32
		Instance                          windows.Handle
		IDList                            unsafe.Pointer
		Class                             *uint16
		ClassKey                          windows.Handle
		HotKey                            uint32
		Icon, Process                     windows.Handle
	}
	info := shellExecuteInfo{Mask: 0x40 | 0x100, Verb: verb, File: file, Parameters: params, Directory: directory, Show: windows.SW_SHOWNORMAL}
	info.Size = uint32(unsafe.Sizeof(info))
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil {
		return err
	}
	defer windows.CoUninitialize()
	result, _, callErr := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return fmt.Errorf("administrator approval failed: %w", callErr)
	}
	if info.Process == 0 {
		return errors.New("elevated installer process handle missing")
	}
	defer windows.CloseHandle(info.Process)
	if _, err := windows.WaitForSingleObject(info.Process, windows.INFINITE); err != nil {
		return err
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(info.Process, &exitCode); err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("elevated installer exited with code %d; installation was not accepted", exitCode)
	}
	return nil
}
