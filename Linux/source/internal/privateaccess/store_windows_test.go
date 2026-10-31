// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/productprofile"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/sys/windows"
)

func privateTestDirectory(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile")
	directory, err := winfile.OpenDirectory(path, true)
	must(t, err)
	must(t, directory.Close())
	return path
}

func TestWindowsPrivateWriterLockAndIdentityBinding(t *testing.T) {
	s, dir, host := fixture(t)
	if other, err := Open(dir, host); err == nil {
		other.Close()
		t.Fatal("two writers allowed")
	}
	if err := os.Rename(dir, dir+"-moved"); err == nil {
		t.Fatal("pinned profile directory was renamed")
	}
	must(t, s.Close())
	if other, err := Open(dir, identity(t)); !errors.Is(err, ErrInvalid) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("host identity mismatch: %v", err)
	}
	reopened, err := Open(dir, host)
	must(t, err)
	must(t, reopened.Close())
}

func TestWindowsPrivateStoreRejectsUnsafeStateWithoutRepair(t *testing.T) {
	for _, filename := range []string{Filename, LockFilename} {
		for _, kind := range []string{"public", "hardlink", "symlink", "directory"} {
			t.Run(filename+"/"+kind, func(t *testing.T) {
				s, dir, host := fixture(t)
				must(t, s.Close())
				path := filepath.Join(dir, filename)
				before, err := os.ReadFile(path)
				must(t, err)
				target := path
				if kind == "public" {
					sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
					must(t, err)
					acl, _, err := sd.DACL()
					must(t, err)
					must(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil))
				} else {
					target = path + "-original"
					must(t, os.Rename(path, target))
					switch kind {
					case "hardlink":
						must(t, os.Link(target, path))
					case "symlink":
						must(t, os.Symlink(target, path))
					case "directory":
						must(t, os.Mkdir(path, 0o700))
					}
				}
				if other, err := Open(dir, host); err == nil {
					other.Close()
					t.Fatal("unsafe state accepted")
				}
				after, err := os.ReadFile(target)
				must(t, err)
				if !bytes.Equal(before, after) {
					t.Fatal("unsafe evidence modified")
				}
			})
		}
	}
	if s, err := Open(t.TempDir(), identity(t)); !errors.Is(err, winfile.ErrUnsafe) {
		if s != nil {
			s.Close()
		}
		t.Fatalf("inherited directory accepted: %v", err)
	}
	path := privateTestDirectory(t)
	alias := filepath.Join(t.TempDir(), "alias")
	must(t, os.Symlink(path, alias))
	if s, err := Open(alias, identity(t)); err == nil {
		s.Close()
		t.Fatal("reparse directory accepted")
	}
}

func TestWindowsPrivatePersistenceFailureClosesAdmission(t *testing.T) {
	s, dir, _ := fixture(t)
	client := identity(t)
	grant(t, s, client)
	policy, err := NewPolicy(s)
	must(t, err)
	defer policy.Close()
	permit, err := policy.Acquire(context.Background(), client)
	must(t, err)
	permit.Release()
	before, err := os.ReadFile(filepath.Join(dir, Filename))
	must(t, err)
	busy, err := os.Open(filepath.Join(dir, Filename))
	must(t, err)
	defer busy.Close()
	if err := s.Revoke(client); err == nil {
		t.Fatal("busy replacement succeeded")
	}
	if _, err := s.GrantRevision(client); !errors.Is(err, ErrClosed) {
		t.Fatal("failed store grants access")
	}
	if _, err := policy.Acquire(context.Background(), client); err == nil {
		t.Fatal("failed store accepted admission")
	}
	select {
	case <-permit.Context.Done():
	default:
		t.Fatal("failed store did not invalidate cached grant")
	}
	must(t, busy.Close())
	after, err := os.ReadFile(filepath.Join(dir, Filename))
	must(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("failed write lost prior state")
	}
	entries, err := os.ReadDir(dir)
	must(t, err)
	if len(entries) != 2 {
		t.Fatal("temporary write residue")
	}
}

func TestWindowsPrivateStoreCrashChild(t *testing.T) {
	if os.Getenv("IRVIO_PRIVATE_CRASH_CHILD") != "1" {
		return
	}
	host, err := peer.Decode(os.Getenv("IRVIO_PRIVATE_CRASH_HOST"))
	must(t, err)
	s, err := Open(os.Getenv("IRVIO_PRIVATE_CRASH_DIR"), host)
	must(t, err)
	defer s.Close()
	_, err = s.CreateAccessInvitation(time.Now(), time.Hour)
	must(t, err)
	fmt.Println("PRIVATE_STORE_LOCKED")
	for {
		time.Sleep(time.Second)
	}
}

func TestWindowsPrivateStoreKillReleasesWriterWithoutReset(t *testing.T) {
	s, dir, host := fixture(t)
	client := identity(t)
	grant(t, s, client)
	must(t, s.Close())
	executable, err := os.Executable()
	must(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWindowsPrivateStoreCrashChild$")
	cmd.Env = append(os.Environ(), "IRVIO_PRIVATE_CRASH_CHILD=1", "IRVIO_PRIVATE_CRASH_HOST="+host.String(), "IRVIO_PRIVATE_CRASH_DIR="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	must(t, err)
	must(t, cmd.Start())
	waited := false
	defer func() {
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "PRIVATE_STORE_LOCKED" {
		t.Fatalf("child not ready: %v", scanner.Err())
	}
	if other, err := Open(dir, host); err == nil {
		other.Close()
		t.Fatal("concurrent process acquired writer")
	}
	must(t, cmd.Process.Kill())
	if err := cmd.Wait(); err == nil {
		t.Fatal("forced termination exited successfully")
	}
	waited = true
	reopened, err := Open(dir, host)
	must(t, err)
	defer reopened.Close()
	state, err := reopened.Snapshot()
	must(t, err)
	if !state.Authorized(client) || len(state.Invitations) != 2 {
		t.Fatal("crash reset acknowledged state")
	}
}

func TestWindowsPrivateProfileRepeatedCloseDelete(t *testing.T) {
	previousGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previousGC)
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	path, host := filepath.Join(t.TempDir(), "profile"), identity(t)
	cycle := func() {
		profile, err := productprofile.Open(path)
		must(t, err)
		s, err := Open(path, host)
		must(t, err)
		_, err = s.CreateAccessInvitation(time.Now(), time.Hour)
		must(t, err)
		if err := profile.DeleteContents(); err == nil {
			s.Close()
			t.Fatal("active private store deleted")
		}
		must(t, s.Close())
		must(t, profile.DeleteContents())
		entries, err := os.ReadDir(path)
		must(t, err)
		if len(entries) != 0 {
			t.Fatal("profile residue")
		}
	}
	for range 20 {
		cycle()
	}
	count := func() uint32 {
		var value uint32
		result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&value)))
		if result == 0 {
			t.Fatal(err)
		}
		return value
	}
	before := count()
	for range 100 {
		cycle()
	}
	after := count()
	t.Logf("100 private open/mutate/refuse-delete/close/delete cycles: handles %d -> %d", before, after)
	if after > before+4 {
		t.Fatalf("private handles grew beyond runtime tolerance: %d -> %d", before, after)
	}
}
