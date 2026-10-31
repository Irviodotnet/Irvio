// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func TestWindowsNativeProductPrivateClient(t *testing.T) {
	path := os.Getenv("IRVIO_WINDOWS_PRIVATE_INVITATION_FILE")
	if path == "" {
		t.Skip("explicit mixed-platform invitation fixture required")
	}
	requireWindowsProductFixture(t)
	invitation, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(os.Getenv("IRVIO_WINDOWS_PRIVATE_EGRESS_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client, err := localapi.Dial(ctx, localapi.DefaultSocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		if logs, err := client.GetRecentLogs(cleanup, localapi.MaxLogTailRecords); err == nil {
			for _, record := range logs.Records {
				t.Logf("daemon: %v", record)
			}
		}
		if err := client.Disconnect(cleanup); err != nil {
			t.Error("cleanup disconnect:", err)
		}
	}()
	result, err := client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_IMPORT_INVITATION, InvitationLink: strings.TrimSpace(string(invitation)), Name: "Private platform test host"})
	if err != nil {
		t.Fatal("import invitation:", err)
	}
	target := result.TargetPeerId
	if target == "" {
		t.Fatal("missing imported target")
	}
	started := time.Now()
	result, err = client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_RESUME_ENROLLMENT, PeerId: target})
	if err != nil {
		t.Fatal("automatic enrollment:", err)
	}
	t.Logf("enrollment elapsed=%s result=%s", time.Since(started), result.EnrollmentState)
	state, err := client.GetPrivateState(ctx, 0, 64)
	if err != nil || !slices.ContainsFunc(state.GetTargets(), func(saved *localv1.PrivateTarget) bool { return saved.GetPeerId() == target }) {
		t.Fatalf("target not saved: %v %v", state, err)
	}
	status, err := client.GetStatus(ctx)
	if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("enrollment must not auto-connect: %v %v", status, err)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	for iteration := 1; iteration <= 2; iteration++ {
		started = time.Now()
		if _, err := client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, PeerId: target}); err != nil {
			t.Fatal("private CONNECT:", err)
		}
		for {
			status, err = client.GetStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if status.State == localv1.ConnectionState_CONNECTION_STATE_CONNECTED {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("private CONNECT timeout:", status)
			case <-time.After(time.Second):
			}
		}
		if status.PrivateTargetPeerId != target || status.CountryCode != "" || status.PrivateHostingWanted {
			t.Fatalf("wrong Private intent: %v", status)
		}
		t.Logf("iteration=%d exact-target CONNECTED after %s", iteration, time.Since(started))
		if addresses, err := net.DefaultResolver.LookupIPAddr(ctx, "example.com"); err != nil || len(addresses) == 0 {
			t.Fatalf("native DNS failed: %v %v", addresses, err)
		}
		for _, probe := range []struct{ url, expected string }{{"https://api.ipify.org", strings.TrimSpace(string(expected))}, {"https://speed.cloudflare.com/__down?bytes=1048576", ""}} {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := httpClient.Do(request)
			if err != nil {
				t.Fatal("private user traffic:", err)
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1048577))
			closeErr := response.Body.Close()
			if response.StatusCode != 200 || readErr != nil || closeErr != nil {
				t.Fatalf("probe status=%d read=%v close=%v", response.StatusCode, readErr, closeErr)
			}
			if probe.expected != "" && strings.TrimSpace(string(body)) != probe.expected || probe.expected == "" && len(body) != 1048576 {
				t.Fatalf("wrong probe response, length=%d", len(body))
			}
		}
		t.Logf("iteration=%d DNS, no-proxy HTTPS and 1 MiB download PASS; awaiting host-side counter observation", iteration)
		time.Sleep(10 * time.Second)
		if err := client.Disconnect(ctx); err != nil {
			t.Fatal(err)
		}
		status, err = client.GetStatus(ctx)
		if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
			t.Fatalf("not idle after private disconnect: %v %v", status, err)
		}
		transport.CloseIdleConnections()
		t.Logf("iteration=%d disconnected; saved grant reused without enrollment next time", iteration)
	}
}
