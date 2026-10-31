// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

func TestControlCrashHelper(t *testing.T) {
	if os.Getenv("IRVIO_CONTROL_CRASH_HELPER") != "1" {
		t.Skip("child process only")
	}
	luid, err := strconv.ParseUint(os.Getenv("IRVIO_CONTROL_LUID"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var m *OwnedControl
	if endpoint := os.Getenv("IRVIO_CONTROL_PHYSICAL"); endpoint != "" {
		local, parseErr := netip.ParseAddrPort(endpoint)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		socket, listenErr := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(local))
		if listenErr != nil {
			t.Fatal(listenErr)
		}
		defer socket.Close()
		m, err = OpenOwnedInbound(os.Getenv("IRVIO_CONTROL_JOURNAL"), luid, socket.LocalAddr().(*net.UDPAddr).AddrPort(), 17)
	} else {
		m, err = OpenOwnedControl(os.Getenv("IRVIO_CONTROL_JOURNAL"), luid, netip.MustParseAddrPort("10.253.244.1:7890"), netip.MustParsePrefix("10.253.244.0/24"))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Install(); err != nil {
		m.Close()
		t.Fatal(err)
	}
	fmt.Println("CONTROL_CRASH_READY")
	for {
		time.Sleep(time.Hour)
	}
}

func TestNativePhysicalCrashRecovery(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_INBOUND_TEST") != "1" {
		t.Skip("requires isolated elevated Windows VM")
	}
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "physical")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestControlCrashHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "IRVIO_CONTROL_CRASH_HELPER=1", "IRVIO_CONTROL_LUID="+strconv.FormatUint(path.Binding.LUID, 10), "IRVIO_CONTROL_JOURNAL="+directory, "IRVIO_CONTROL_PHYSICAL="+netip.AddrPortFrom(path.Binding.Source, 0).String())
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
		if err := RecoverControl(directory); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(pipe)
	ready := false
	for scanner.Scan() {
		if scanner.Text() == "CONTROL_CRASH_READY" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("physical crash helper not ready", scanner.Err())
	}
	if err := RecoverControl(directory); err == nil {
		t.Fatal("recovery stole live socket journal")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err == nil {
		t.Fatal("crash helper exited cleanly")
	}
	j, err := openControlJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	record, err := j.Read()
	j.Close()
	if err != nil || record.Rule == nil || !record.Rule.Physical {
		t.Fatal("crash lost physical rule journal", err)
	}
	if err := withRules(func(rules *object) error {
		r, err := findRule(rules, record.Rule.control().name)
		if err != nil {
			return err
		}
		if r == nil {
			return fmt.Errorf("physical rule disappeared before recovery")
		}
		defer r.release()
		return record.Rule.control().verify(r)
	}); err != nil {
		t.Fatal(err)
	}
	if err := RecoverControl(directory); err != nil {
		t.Fatal(err)
	}
	if err := withRules(func(rules *object) error {
		r, err := findRule(rules, record.Rule.control().name)
		if r != nil {
			r.release()
			return fmt.Errorf("physical crash rule retained")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeControlCrashRecovery(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_FIREWALL_TEST") != "1" {
		t.Skip("requires elevated isolated Windows VM")
	}
	for _, gone := range []bool{false, true} {
		t.Run(fmt.Sprintf("adapter_gone_%v", gone), func(t *testing.T) {
			tun, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-FWCRASH", Addresses: []netip.Prefix{netip.MustParsePrefix("10.253.244.1/24")}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := tun.Close(); err != nil {
					t.Error(err)
				}
			})
			path := filepath.Join(t.TempDir(), "private")
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestControlCrashHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "IRVIO_CONTROL_CRASH_HELPER=1", "IRVIO_CONTROL_LUID="+strconv.FormatUint(tun.LUID(), 10), "IRVIO_CONTROL_JOURNAL="+path)
			pipe, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if !waited {
					cmd.Process.Kill()
					cmd.Wait()
				}
				if err := RecoverControl(path); err != nil {
					t.Error(err)
				}
			}()
			scanner := bufio.NewScanner(pipe)
			ready := false
			for scanner.Scan() {
				if scanner.Text() == "CONTROL_CRASH_READY" {
					ready = true
					break
				}
			}
			if !ready {
				t.Fatal("crash child not ready", scanner.Err())
			}
			if err := RecoverControl(path); err == nil {
				t.Fatal("recovery stole live journal ownership")
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			if err == nil {
				t.Fatal("child exited cleanly")
			}
			j, err := openControlJournal(path)
			if err != nil {
				t.Fatal(err)
			}
			r, err := j.Read()
			closeErr := j.Close()
			if err != nil || closeErr != nil || r.Rule == nil {
				t.Fatal("crash journal missing", err, closeErr)
			}
			if err := withRules(func(rules *object) error {
				rule, err := findRule(rules, r.Rule.control().name)
				if err != nil {
					return err
				}
				if rule == nil {
					return fmt.Errorf("crash rule missing")
				}
				defer rule.release()
				return r.Rule.control().verify(rule)
			}); err != nil {
				t.Fatal(err)
			}
			setPort := func(port string) error {
				return withRules(func(rules *object) error {
					rule, err := findRule(rules, r.Rule.control().name)
					if err != nil {
						return err
					}
					if rule == nil {
						return fmt.Errorf("fixture rule missing")
					}
					defer rule.release()
					return rule.putString(18, port)
				})
			}
			if err := setPort("7891"); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(RecoverControl(path), ErrConflict) {
				t.Fatal("recovery accepted changed port")
			}
			j, err = openControlJournal(path)
			if err != nil {
				t.Fatal(err)
			}
			retained, readErr := j.Read()
			closeErr = j.Close()
			if readErr != nil || closeErr != nil || retained.Rule == nil || *retained.Rule != *r.Rule {
				t.Fatal("foreign-scope failure discarded journal", readErr, closeErr)
			}
			if err := setPort("7890"); err != nil {
				t.Fatal(err)
			}
			if gone {
				if err := tun.Close(); err != nil {
					t.Fatal(err)
				}
				if err := RecoverControl(path); err != nil {
					t.Fatal("recovery", err)
				}
			} else {
				next, err := OpenOwnedControl(path, tun.LUID(), r.Rule.Endpoint, r.Rule.Pool)
				if err != nil {
					t.Fatal("replacement owner", err)
				}
				if err := next.Install(); err != nil {
					next.Close()
					t.Fatal(err)
				}
				if next.scope.Owner == r.Rule.Owner {
					t.Fatal("replacement reused old owner")
				}
				if err := next.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := RecoverControl(path); err != nil {
				t.Fatal("idempotent recovery", err)
			}
			if err := withRules(func(rules *object) error {
				rule, err := findRule(rules, r.Rule.control().name)
				if rule != nil {
					rule.release()
					return fmt.Errorf("rule residue")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			t.Log("real owner process kill -> exact journal recovery -> zero rule residue PASS")
		})
	}
}
