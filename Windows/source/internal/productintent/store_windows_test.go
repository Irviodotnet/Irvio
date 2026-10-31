// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productintent

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"
	"golang.org/x/sys/windows"
)

func fixture(t *testing.T) Store {
	t.Helper()
	return Store{Path: filepath.Join(t.TempDir(), "runtime", sessionFilename), session: func() (scmSession, error) {
		return scmSession{PID: 100, Created: 200}, nil
	}}
}

func assertState(t *testing.T, s Store, want State) {
	t.Helper()
	state, present, err := s.Read()
	if err != nil || !present || state != want {
		t.Fatalf("Read = %+v, %v, %v; want %+v", state, present, err, want)
	}
}

func TestWindowsIntentLifecycle(t *testing.T) {
	s := fixture(t)
	if _, present, err := s.Read(); err != nil || present {
		t.Fatalf("empty %v %v", present, err)
	}
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if err := s.Want("NL"); err != nil {
			t.Fatal(err)
		}
		assertState(t, s, State{Version: 1, ConnectIntent: true, Country: "NL"})
		for _, mode := range []struct {
			target  peer.ID
			hosting bool
		}{{id, false}, {"", true}, {id, true}} {
			if err := s.WantPrivate(mode.target, mode.hosting); err != nil {
				t.Fatal(err)
			}
			want := State{Version: 2, PrivateHosting: mode.hosting, ConnectIntent: mode.target != ""}
			if mode.target != "" {
				want.PrivateTarget = mode.target.String()
			}
			assertState(t, s, want)
		}
		if err := s.BeginTeardown(); err != nil {
			t.Fatal(err)
		}
		assertState(t, s, State{Version: 1, TeardownPending: true})
		if err := s.Clear(); err != nil {
			t.Fatal(err)
		}
		if _, present, err := s.Read(); err != nil || present {
			t.Fatalf("cleared %v %v", present, err)
		}
		entries, err := os.ReadDir(filepath.Dir(s.Path))
		if err != nil || len(entries) != 1 || entries[0].Name() != sessionLock {
			t.Fatalf("residue: %v %v", entries, err)
		}
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsIntentPreviousBootRequiresCleanup(t *testing.T) {
	s := fixture(t)
	if err := s.Want("NL"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []scmSession{{PID: 100, Created: 201}, {PID: 101, Created: 200}} {
		s.session = func() (scmSession, error) { return next, nil }
		state, present, err := s.Read()
		if !errors.Is(err, ErrPreviousBoot) || present || state != (State{}) {
			t.Fatalf("previous boot restored: %+v %v %v", state, present, err)
		}
		if err := s.Want("DE"); !errors.Is(err, ErrPreviousBoot) {
			t.Fatalf("overwrote previous boot: %v", err)
		}
		if err := s.BeginTeardown(); !errors.Is(err, ErrPreviousBoot) {
			t.Fatalf("overwrote previous residue: %v", err)
		}
		after, err := os.ReadFile(s.Path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("previous boot state modified", err)
		}
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := s.Want("DE"); err != nil {
		t.Fatal(err)
	}
	assertState(t, s, State{Version: 1, ConnectIntent: true, Country: "DE"})
}

func replaceFixture(t *testing.T, s Store, data []byte) {
	t.Helper()
	d, err := winfile.OpenDirectory(filepath.Dir(s.Path), false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.WriteAtomic(sessionFilename, data, true); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsIntentRejectsInvalidData(t *testing.T) {
	s := fixture(t)
	if err := s.Want("NL"); err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		"{", "{}", string(valid) + "{}", strings.Repeat(" ", maxEnvelopeBytes+1),
		strings.Replace(string(valid), `"version":1`, `"version":99`, 1),
		strings.Replace(string(valid), `"pid":100`, `"pid":0`, 1),
		strings.Replace(string(valid), `"country":"NL"`, `"country":"nl"`, 1),
		strings.Replace(string(valid), `"country":"NL"`, `"country":"NL","private_hosting":true`, 1),
		strings.Replace(string(valid), `"country":"NL"`, `"country":"NL","unknown":1`, 1),
		strings.Replace(string(valid), `"session":`, `"unknown":0,"session":`, 1),
	} {
		replaceFixture(t, s, []byte(data))
		if _, _, err := s.Read(); err == nil {
			t.Fatalf("accepted %q", data)
		}
		if err := s.Want("DE"); err == nil {
			t.Fatal("overwrote invalid intent")
		}
		after, err := os.ReadFile(s.Path)
		if err != nil || string(after) != data {
			t.Fatal("invalid intent modified", err)
		}
	}
	replaceFixture(t, s, valid)
	large, err := multihash.Encode(make([]byte, 1024), multihash.IDENTITY)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WantPrivate(peer.ID(large), false); err == nil {
		t.Fatal("accepted oversized private intent")
	}
	for _, operation := range []func() error{func() error { return s.Want("nl") }, func() error { return s.WantPrivate("invalid", false) }, func() error { return s.WantPrivate("", false) }} {
		if err := operation(); err == nil {
			t.Fatal("accepted invalid request")
		}
	}
	assertState(t, s, State{Version: 1, ConnectIntent: true, Country: "NL"})
	s.session = func() (scmSession, error) { return scmSession{}, os.ErrNotExist }
	if _, _, err := s.Read(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost query failure: %v", err)
	}
	if err := s.Want("DE"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost write query failure: %v", err)
	}
}

func TestWindowsIntentRejectsUnsafeState(t *testing.T) {
	for _, kind := range []string{"file-acl", "directory-acl", "lock-acl", "hardlink", "symlink", "directory-symlink", "readonly", "busy"} {
		t.Run(kind, func(t *testing.T) {
			s := fixture(t)
			if err := s.Want("NL"); err != nil {
				t.Fatal(err)
			}
			preserved := s.Path
			switch kind {
			case "file-acl", "directory-acl", "lock-acl":
				path := s.Path
				if kind == "directory-acl" {
					path = filepath.Dir(s.Path)
				}
				if kind == "lock-acl" {
					path = filepath.Join(filepath.Dir(s.Path), sessionLock)
				}
				sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
				if err != nil {
					t.Fatal(err)
				}
				acl, _, err := sd.DACL()
				if err != nil {
					t.Fatal(err)
				}
				if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(s.Path, s.Path+"-alias"); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				preserved = s.Path + "-original"
				if err := os.Rename(s.Path, preserved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(preserved, s.Path); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				original := filepath.Dir(s.Path) + "-original"
				if err := os.Rename(filepath.Dir(s.Path), original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, filepath.Dir(s.Path)); err != nil {
					t.Fatal(err)
				}
				preserved = filepath.Join(original, sessionFilename)
			case "readonly":
				p, err := windows.UTF16PtrFromString(s.Path)
				if err != nil {
					t.Fatal(err)
				}
				if err := windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_READONLY); err != nil {
					t.Fatal(err)
				}
				defer windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_NORMAL)
			case "busy":
				d, err := winfile.OpenDirectory(filepath.Dir(s.Path), false)
				if err != nil {
					t.Fatal(err)
				}
				defer d.Close()
				lock, err := d.OpenFile(sessionLock, true, false)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				if err := winfile.Lock(lock); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(preserved)
			if err != nil {
				t.Fatal(err)
			}
			if kind != "readonly" {
				if _, _, err := s.Read(); err == nil {
					t.Fatal("read unsafe state")
				}
			}
			if err := s.Want("DE"); err == nil {
				t.Fatal("replaced unsafe state")
			}
			if err := s.Clear(); err == nil {
				t.Fatal("removed unsafe state")
			}
			after, err := os.ReadFile(preserved)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("unsafe target modified", err)
			}
		})
	}
}

func requireNative(t *testing.T) {
	t.Helper()
	if os.Getenv("IRVIO_INTENT_NATIVE") != "1" {
		t.Skip("requires SYSTEM lifecycle harness")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user.User.Sid.String() != "S-1-5-18" {
		t.Fatal("native intent harness must run as SYSTEM", err)
	}
}

func TestWindowsIntentCrashChild(t *testing.T) {
	path := os.Getenv("IRVIO_INTENT_CHILD_PATH")
	if path == "" {
		t.Skip("child process only")
	}
	s := Store{Path: path}
	var err error
	if os.Getenv("IRVIO_INTENT_CHILD_TEARDOWN") == "1" {
		err = s.BeginTeardown()
	} else {
		err = s.Want("NL")
	}
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("INTENT_READY")
	for {
		time.Sleep(time.Second)
	}
}

func TestWindowsSCMDecoyChild(t *testing.T) {
	if os.Getenv("IRVIO_SCM_DECOY") != "1" {
		t.Skip("decoy child only")
	}
	fmt.Println("DECOY_READY")
	for {
		time.Sleep(time.Second)
	}
}

func TestWindowsNativeSCMIdentity(t *testing.T) {
	requireNative(t)
	first, err := currentSCMSession()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspectSCM(uint32(os.Getpid()), filepath.Join(dir, "services.exe")); !errors.Is(err, errNotSCM) {
		t.Fatalf("accepted non-SCM process: %v", err)
	}
	payload, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(t.TempDir(), "services.exe")
	if err := os.WriteFile(decoy, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(decoy, "-test.run=^TestWindowsSCMDecoyChild$")
	cmd.Env = append(os.Environ(), "IRVIO_SCM_DECOY=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "DECOY_READY" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("decoy failed")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("decoy timeout")
	}
	prior := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prior)
	handles := func() uint32 {
		var count uint32
		ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			t.Fatal(err)
		}
		return count
	}
	for range 20 {
		if _, err := currentSCMSession(); err != nil {
			t.Fatal(err)
		}
	}
	baseline := handles()
	for range 100 {
		current, err := currentSCMSession()
		if err != nil || current != first {
			t.Fatalf("SCM identity unstable: %+v %+v %v", first, current, err)
		}
	}
	last := handles()
	if last > baseline+8 {
		t.Fatalf("native handle growth %d -> %d", baseline, last)
	}
	t.Logf("100 SCM lookups with decoy: handles %d -> %d", baseline, last)
	path, err := DefaultStatePath()
	if err != nil || !filepath.IsAbs(path) || filepath.Base(path) != sessionFilename {
		t.Fatalf("default path %q %v", path, err)
	}
}

func TestWindowsNativeIntentSurvivesCrash(t *testing.T) {
	requireNative(t)
	s := Store{Path: fixture(t).Path}
	for _, teardown := range []bool{false, true} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsIntentCrashChild$")
		cmd.Env = append(os.Environ(), "IRVIO_INTENT_CHILD_PATH="+s.Path, fmt.Sprintf("IRVIO_INTENT_CHILD_TEARDOWN=%d", map[bool]int{true: 1, false: 0}[teardown]))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		ready := make(chan bool, 1)
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				if scanner.Text() == "INTENT_READY" {
					ready <- true
					return
				}
			}
			ready <- false
		}()
		ok := false
		select {
		case ok = <-ready:
		case <-time.After(15 * time.Second):
		}
		killErr := cmd.Process.Kill()
		waitErr := cmd.Wait()
		if !ok || killErr != nil || waitErr == nil {
			t.Fatalf("child kill: ready=%v kill=%v wait=%v stderr=%s", ok, killErr, waitErr, stderr.String())
		}
		want := State{Version: 1, ConnectIntent: true, Country: "NL"}
		if teardown {
			want = State{Version: 1, TeardownPending: true}
		}
		assertState(t, s, want)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
}

// The harness preserves this fixture across an actual VM reboot, never a simulated timestamp change.
func TestWindowsNativeIntentBootFixture(t *testing.T) {
	mode := os.Getenv("IRVIO_INTENT_BOOT_MODE")
	if mode == "" {
		t.Skip("reboot harness only")
	}
	requireNative(t)
	s := Store{Path: os.Getenv("IRVIO_INTENT_BOOT_PATH")}
	epoch, err := currentSCMSession()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(epoch)
	t.Logf("SCM_SESSION %s", encoded)
	switch mode {
	case "seed":
		if _, present, err := s.Read(); err != nil || present {
			t.Fatalf("seed fixture exists %v %v", present, err)
		}
		if err := s.Want("NL"); err != nil {
			t.Fatal(err)
		}
		assertState(t, s, State{Version: 1, ConnectIntent: true, Country: "NL"})
	case "same":
		assertState(t, s, State{Version: 1, ConnectIntent: true, Country: "NL"})
	case "new":
		state, present, err := s.Read()
		if !errors.Is(err, ErrPreviousBoot) || present || state != (State{}) {
			t.Fatalf("new boot restored intent %+v %v %v", state, present, err)
		}
		if err := s.Want("DE"); !errors.Is(err, ErrPreviousBoot) {
			t.Fatalf("new boot overwrite %v", err)
		}
		if err := s.Clear(); err != nil {
			t.Fatal(err)
		}
		if _, present, err := s.Read(); err != nil || present {
			t.Fatalf("cleanup %v %v", present, err)
		}
	default:
		t.Fatal("unknown boot fixture mode")
	}
}
