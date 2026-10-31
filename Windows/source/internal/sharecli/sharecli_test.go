//go:build linux

// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sharecli

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

func networkCode(t *testing.T) string {
	t.Helper()
	k, _, e := crypto.GenerateEd25519Key(nil)
	if e != nil {
		t.Fatal(e)
	}
	p, e := peer.IDFromPrivateKey(k)
	if e != nil {
		t.Fatal(e)
	}
	code, e := productbootstrap.Encode(&localv1.BootstrapBundle{FormatVersion: 1, Nodes: []*localv1.BootstrapNode{{PeerId: p.String(), Endpoints: []*localv1.BootstrapEndpoint{{Ip: []byte{89, 125, 166, 144}, Port: 4000}}}}})
	if e != nil {
		t.Fatal(e)
	}
	return code
}

func TestReadBootstrapInputs(t *testing.T) {
	code := networkCode(t)
	dir := t.TempDir()
	text := filepath.Join(dir, "bootstrap.txt")
	if e := os.WriteFile(text, []byte("\ufeff"+code+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	qr, e := qrcode.NewQRCodeWriter().Encode(code, gozxing.BarcodeFormat_QR_CODE, 600, 600, nil)
	if e != nil {
		t.Fatal(e)
	}
	image := filepath.Join(dir, "bootstrap.png")
	f, e := os.Create(image)
	if e != nil {
		t.Fatal(e)
	}
	if e = png.Encode(f, qr); e != nil {
		t.Fatal(e)
	}
	f.Close()
	for _, input := range []string{code, text, image} {
		got, e := ReadBootstrap(input)
		if e != nil || got != code {
			t.Fatalf("input roundtrip: %v", e)
		}
	}
	for _, input := range []string{"irvio-bootstrap:v1:bad", dir, filepath.Join(dir, "missing")} {
		if _, e := ReadBootstrap(input); e == nil {
			t.Fatal("accepted invalid input")
		}
	}
	fifo := filepath.Join(dir, "fifo")
	if e := syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadBootstrap(fifo); e == nil {
		t.Fatal("accepted FIFO")
	}
	big := filepath.Join(dir, "big")
	f, e = os.Create(big)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(maxFileBytes + 1); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, e := ReadBootstrap(big); e == nil {
		t.Fatal("accepted oversized input")
	}
}

type fakeAPI struct {
	state          localv1.ConnectionState
	hosting        bool
	link           string
	failStart      bool
	canceledStart  bool
	stop           context.CancelFunc
	disconnected   int
	imported       bool
	persistent     bool
	failPersistent bool
}

func (f *fakeAPI) GetStatus(context.Context) (*localv1.Status, error) {
	return &localv1.Status{State: f.state, PrivateHostingWanted: f.hosting}, nil
}
func (f *fakeAPI) ImportBootstrapBundle(context.Context, string) (*localv1.BootstrapState, error) {
	f.imported = true
	return &localv1.BootstrapState{}, nil
}
func (f *fakeAPI) Disconnect(context.Context) error {
	f.disconnected++
	f.hosting = false
	f.persistent = false
	f.state = localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED
	return nil
}
func (f *fakeAPI) PrivateCommand(ctx context.Context, c *localv1.PrivateCommand) (*localv1.PrivateCommandResult, error) {
	switch c.Action {
	case localv1.PrivateAction_PRIVATE_ACTION_SET_PERSISTENT_HOSTING:
		if f.failPersistent {
			return nil, errors.New("persistence failed")
		}
		f.persistent = c.Hosting
	case localv1.PrivateAction_PRIVATE_ACTION_CONNECT:
		if f.failStart {
			return nil, errors.New("start failed")
		}
		f.hosting = true
		f.state = localv1.ConnectionState_CONNECTION_STATE_HOSTING
		if f.canceledStart {
			return nil, context.DeadlineExceeded
		}
	case localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION:
		if !c.AutoApprove || c.LifetimeSeconds != 3600 {
			return nil, errors.New("wrong invitation policy")
		}
		if f.stop != nil {
			f.stop()
		}
		return &localv1.PrivateCommandResult{InvitationLink: f.link}, nil
	}
	return &localv1.PrivateCommandResult{}, nil
}

func friendCode(t *testing.T) string {
	t.Helper()
	k, _, e := crypto.GenerateEd25519Key(nil)
	if e != nil {
		t.Fatal(e)
	}
	p, e := peer.IDFromPrivateKey(k)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	code, e := privateaccess.EncodeInvitation(k, privateaccess.InvitationToken{Host: p, Locator: [32]byte{1}, Invitation: privateaccess.Invitation{ID: strings.Repeat("12", 16), Secret: strings.Repeat("34", 32), CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(), State: "open"}}, now)
	if e != nil {
		t.Fatal(e)
	}
	return code
}
func TestRunLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api := &fakeAPI{state: localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED, link: friendCode(t), stop: cancel}
	var out bytes.Buffer
	dir := t.TempDir()
	if e := Run(ctx, api, networkCode(t), dir, &out); e != nil {
		t.Fatal(e)
	}
	if api.disconnected != 0 || !api.hosting || !api.imported {
		t.Fatal("incorrect lifecycle")
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 {
		t.Fatal("missing invitation")
	}
	path := filepath.Join(dir, entries[0].Name())
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe permissions")
	}
	data, e := os.ReadFile(path)
	if e != nil || string(data) != api.link+"\n" {
		t.Fatal("incorrect invitation")
	}
	if strings.Contains(out.String(), api.link) {
		t.Fatal("secret printed")
	}
}

func TestDetachedSharingStatusAndStop(t *testing.T) {
	api := &fakeAPI{state: localv1.ConnectionState_CONNECTION_STATE_HOSTING, hosting: true}
	var out bytes.Buffer
	if err := Status(context.Background(), api, &out); err != nil || !strings.Contains(out.String(), "Private sharing: true") {
		t.Fatalf("status: %v %s", err, out.String())
	}
	if err := Stop(context.Background(), api, &out); err != nil || api.hosting || api.disconnected != 1 {
		t.Fatalf("stop: %v", err)
	}
	if err := Stop(context.Background(), api, &out); err != nil || api.disconnected != 1 {
		t.Fatal("stop is not idempotent")
	}
	api.state = localv1.ConnectionState_CONNECTION_STATE_CONNECTED
	if err := Stop(context.Background(), api, &out); err == nil || api.disconnected != 1 {
		t.Fatal("stop interrupted a different VPN mode")
	}
}

func TestRunPersistentSuccessAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		api := &fakeAPI{state: localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED, link: friendCode(t), failPersistent: fail}
		var out bytes.Buffer
		err := RunPersistent(context.Background(), api, networkCode(t), t.TempDir(), &out, true)
		if fail {
			if err == nil || api.hosting || api.persistent || api.disconnected != 1 {
				t.Fatal("persistence failure not cleaned up")
			}
		} else if err != nil || !api.hosting || !api.persistent || api.disconnected != 0 {
			t.Fatalf("persistent start: %v", err)
		}
	}
}
func TestRunFailureSafety(t *testing.T) {
	for _, tc := range []struct {
		name           string
		state          localv1.ConnectionState
		fail, canceled bool
		cleanup        int
	}{
		{"active", localv1.ConnectionState_CONNECTION_STATE_CONNECTED, false, false, 0},
		{"rejected", localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED, true, false, 0},
		{"ambiguous timeout", localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED, false, true, 1},
		{"invalid invitation", localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED, false, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{state: tc.state, failStart: tc.fail, canceledStart: tc.canceled}
			dir := t.TempDir()
			var out bytes.Buffer
			if e := Run(context.Background(), api, networkCode(t), dir, &out); e == nil {
				t.Fatal("expected failure")
			}
			if api.disconnected != tc.cleanup {
				t.Fatal("incorrect cleanup")
			}
			entries, e := os.ReadDir(dir)
			if e != nil || len(entries) != 0 {
				t.Fatal("empty invitation left behind")
			}
			if tc.name == "active" && api.imported {
				t.Fatal("active profile modified")
			}
		})
	}
}
