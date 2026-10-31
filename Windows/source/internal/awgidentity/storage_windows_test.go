// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awgidentity

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/productprofile"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"github.com/Irviodotnet/Irvio/internal/wintest"
	"golang.org/x/sys/windows"
)

func assertIdentityPermissions(t *testing.T, path string) {
	t.Helper()
	dir, err := winfile.OpenDirectory(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	for _, name := range []string{fileName, lockFileName} {
		file, err := dir.OpenFile(name, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func publicACL(t *testing.T, path string) {
	t.Helper()
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
}

func TestWindowsAWGRejectsUnsafeState(t *testing.T) {
	for _, kind := range []string{"key-acl", "parent-acl", "lock-acl", "key-symlink", "parent-symlink", "key-hardlink", "lock-hardlink", "key-directory", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "profile")
			identity, err := LoadOrCreate(path)
			if err != nil {
				t.Fatal(err)
			}
			keyPath := identity.path
			switch kind {
			case "key-acl":
				publicACL(t, keyPath)
			case "parent-acl":
				publicACL(t, path)
			case "lock-acl":
				publicACL(t, filepath.Join(path, lockFileName))
			case "key-symlink":
				target := filepath.Join(path, "preserved-key")
				if err := os.Rename(keyPath, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, keyPath); err != nil {
					t.Fatal(err)
				}
				keyPath = target
			case "parent-symlink":
				target := filepath.Join(parent, "preserved-profile")
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				keyPath = filepath.Join(target, fileName)
			case "key-hardlink":
				if err := os.Link(keyPath, filepath.Join(path, "second-key-link")); err != nil {
					t.Fatal(err)
				}
			case "lock-hardlink":
				if err := os.Link(filepath.Join(path, lockFileName), filepath.Join(path, "second-lock-link")); err != nil {
					t.Fatal(err)
				}
			case "key-directory":
				target := filepath.Join(path, "preserved-key")
				if err := os.Rename(keyPath, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(keyPath, 0700); err != nil {
					t.Fatal(err)
				}
				keyPath = target
			case "truncated":
				if err := os.Truncate(keyPath, fileSize-1); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreate(path); err == nil {
				t.Fatal("unsafe AWG identity accepted")
			}
			if _, _, err := identity.Rotate(); err == nil {
				t.Fatal("rotation repaired unsafe AWG identity")
			}
			after, err := os.ReadFile(keyPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("refusal changed existing key", err)
			}
		})
	}
}

func TestWindowsAWGConcurrentCreationAndStaleRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	dir, err := winfile.OpenDirectory(path, true)
	if err != nil {
		t.Fatal(err)
	}
	dir.Close()
	const count = 16
	results := make(chan *Identity, count)
	failures := make(chan error, count)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			id, err := LoadOrCreate(path)
			if err != nil {
				failures <- err
				return
			}
			results <- id
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			t.Fatal("unexpected creation failure", err)
		}
	}
	winner, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	created := 0
	for id := range results {
		if id.Created {
			created++
		}
		if id.PrivateKey != winner.PrivateKey || id.Epoch != 1 {
			t.Fatal("concurrent creators returned different identities")
		}
	}
	if created != 1 {
		t.Fatalf("created=%d, want one", created)
	}
	stale, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeKey, beforePublic, beforeEpoch := stale.Snapshot()
	if _, _, err := winner.Rotate(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stale.Rotate(); !errors.Is(err, ErrStaleIdentity) {
		t.Fatal("stale rotation not fenced", err)
	}
	key, public, epoch := stale.Snapshot()
	if key != beforeKey || public != beforePublic || epoch != beforeEpoch {
		t.Fatal("failed rotation changed memory")
	}
	current, err := LoadOrCreate(path)
	if err != nil || current.PrivateKey != winner.PrivateKey || current.Epoch != 2 {
		t.Fatal("stale writer changed durable identity", err)
	}
}

func TestWindowsAWGFailedRotationPreservesState(t *testing.T) {
	for _, kind := range []string{"read-only", "busy-file"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profile")
			identity, err := LoadOrCreate(path)
			if err != nil {
				t.Fatal(err)
			}
			key, public, epoch := identity.Snapshot()
			before, err := os.ReadFile(identity.path)
			if err != nil {
				t.Fatal(err)
			}
			dir, err := winfile.OpenDirectory(path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			var restore func()
			if kind == "read-only" {
				if err := os.Chmod(identity.path, 0400); err != nil {
					t.Fatal(err)
				}
				restore = func() {
					if err := os.Chmod(identity.path, 0600); err != nil {
						t.Error(err)
					}
				}
			} else {
				held, err := dir.OpenFile(fileName, false, false)
				if err != nil {
					t.Fatal(err)
				}
				restore = func() { _ = held.Close() }
			}
			defer restore()
			if _, _, err := identity.Rotate(); err == nil {
				t.Fatal("blocked rotation succeeded")
			}
			actualKey, actualPublic, actualEpoch := identity.Snapshot()
			if actualKey != key || actualPublic != public || actualEpoch != epoch {
				t.Fatal("failed rotation changed memory")
			}
			after, err := os.ReadFile(identity.path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed rotation changed durable key", err)
			}
			names, err := dir.Names(10)
			if err != nil || len(names) != 2 {
				t.Fatal("failed rotation left temporary residue", err)
			}
			restore()
			if _, _, err := identity.Rotate(); err != nil {
				t.Fatal("rotation after resource release", err)
			}
		})
	}
}

func TestWindowsAWGLockReleasedOnProcessDeath(t *testing.T) {
	if path := os.Getenv("IRVIO_TEST_AWG_LOCK_PATH"); path != "" {
		err := withIdentityDirectory(path, false, func(*winfile.Directory) error {
			if err := os.WriteFile(os.Getenv("IRVIO_TEST_AWG_LOCK_READY"), []byte("locked"), 0600); err != nil {
				return err
			}
			time.Sleep(time.Minute)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	parent := t.TempDir()
	path, ready := filepath.Join(parent, "profile"), filepath.Join(parent, "ready")
	before, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestWindowsAWGLockReleasedOnProcessDeath$")
	child.Env = append(os.Environ(), "IRVIO_TEST_AWG_LOCK_PATH="+path, "IRVIO_TEST_AWG_LOCK_READY="+ready)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not acquire lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := LoadOrCreate(path); !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		t.Fatal("cross-process writer not fenced", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("child was not terminated")
	}
	after, err := LoadOrCreate(path)
	if err != nil || after.PrivateKey != before.PrivateKey || after.Created {
		t.Fatal("crash lock recovery changed identity", err)
	}
}

func TestWindowsAWGProfileDeletionAndResources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile")
	profile, err := productprofile.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	before := wintest.HandleTypes()
	for range 100 {
		if _, _, err := identity.Rotate(); err != nil {
			t.Fatal(err)
		}
		reloaded, err := LoadOrCreate(path)
		if err != nil || reloaded.PrivateKey != identity.PrivateKey || reloaded.Epoch != identity.Epoch {
			t.Fatal("rotation/reload mismatch", err)
		}
	}
	after := wintest.HandleTypes()
	if err := wintest.ResourceGrowth(before, after); err != nil {
		t.Fatal(err)
	}
	if after["File"] != before["File"] {
		t.Fatal("AWG file handle leak")
	}
	t.Logf("100 rotations/reloads: handles before=%v after=%v", before, after)
	if err := profile.DeleteContents(); err != nil {
		t.Fatal("AWG files block Delete Profile", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatal("profile has residue", err)
	}
}

func TestWindowsAWGNonAdminDenial(t *testing.T) {
	probe := os.Getenv("IRVIO_AWG_ACL_PROBE_EXE")
	if probe == "" {
		t.Skip("explicit non-admin ACL harness required")
	}
	if os.Getenv("IRVIO_ACL_PROBE_USER") == "" || os.Getenv("IRVIO_ACL_PROBE_PASSWORD") == "" {
		t.Fatal("ACL fixture credentials missing")
	}
	path := filepath.Join(t.TempDir(), "профиль-IRVIO")
	identity, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if i == 1 {
			if _, _, err := identity.Rotate(); err != nil {
				t.Fatal(err)
			}
		}
		child := exec.Command(probe, "-test.run=^TestUnprivilegedServiceACL$", "-test.v", "-test.timeout=15s")
		child.Env = append(os.Environ(), "IRVIO_ACL_PROBE_PATH="+identity.path)
		output, err := child.CombinedOutput()
		if err != nil {
			t.Fatalf("native non-admin access probe: %v\n%s", err, output)
		}
		if !bytes.Contains(output, []byte("real non-admin token: read denied, write denied, delete denied")) {
			t.Fatal("non-admin probe did not exercise all access modes")
		}
	}
	t.Log("real non-admin read/write/delete denied before and after AWG key rotation")
}
