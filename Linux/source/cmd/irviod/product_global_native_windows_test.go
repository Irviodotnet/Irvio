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
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestWindowsNativeProductGlobalConnection(t *testing.T) {
	path := os.Getenv("IRVIO_WINDOWS_PRODUCT_BOOTSTRAP_FILE")
	if path == "" {
		t.Skip("disposable product service and explicit bootstrap file required")
	}
	requireWindowsProductFixture(t)
	code, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	country := os.Getenv("IRVIO_WINDOWS_PRODUCT_COUNTRY")
	if country == "" {
		t.Fatal("explicit target country required")
	}
	expectedEgress, err := netip.ParseAddr(os.Getenv("IRVIO_WINDOWS_PRODUCT_EXPECTED_EGRESS"))
	if err != nil || !expectedEgress.IsGlobalUnicast() {
		t.Fatal("explicit expected Exit egress address required")
	}
	crash := os.Getenv("IRVIO_WINDOWS_PRODUCT_CRASH") == "1"
	wanProbe := os.Getenv("IRVIO_WINDOWS_PRODUCT_WAN_PROBE")
	if crash {
		if wanProbe == "" {
			t.Fatal("explicit WAN leak probe required")
		}
		conn, err := net.DialTimeout("tcp", wanProbe, 3*time.Second)
		if err != nil {
			t.Fatal("pre-CONNECT WAN probe unavailable:", err)
		}
		_ = conn.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client, err := localapi.Dial(ctx, localapi.DefaultSocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	leaveForReboot := false
	defer func() {
		if leaveForReboot {
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		if logs, err := client.GetRecentLogs(cleanup, localapi.MaxLogTailRecords); err == nil {
			for _, record := range logs.Records {
				t.Logf("daemon: %v", record)
			}
		} else {
			t.Logf("diagnostic log tail unavailable: %v", err)
		}
		if err := client.Disconnect(cleanup); err != nil {
			t.Errorf("Disconnect: %v", err)
			return
		}
		status, err := client.GetStatus(cleanup)
		if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
			t.Errorf("not IDLE after disconnect: %v %v", status, err)
			return
		}
		if err := client.Shutdown(cleanup); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()
	state, err := client.GetStatus(ctx)
	if err != nil || state.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("initial state=%v err=%v", state, err)
	}
	bootstrap, err := client.ImportBootstrapBundle(ctx, strings.TrimSpace(string(code)))
	if err != nil || bootstrap.ConfiguredCount == 0 {
		t.Fatalf("import=%v err=%v", bootstrap, err)
	}
	t.Logf("bootstrap entries=%d", bootstrap.ConfiguredCount)
	started := time.Now()
	countries, err := client.GetAvailableCountries(ctx, true)
	if err != nil {
		t.Fatal("country discovery:", err)
	}
	t.Logf("country discovery elapsed=%s result=%v", time.Since(started), countries)
	found := false
	for _, entry := range countries.CountryCodes {
		found = found || entry == country
	}
	if !found {
		t.Fatalf("requested country %s is not available", country)
	}
	started = time.Now()
	if err := client.Connect(ctx, country); err != nil {
		t.Fatal("CONNECT:", err)
	}
	for {
		state, err = client.GetStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("connect elapsed=%s state=%v", time.Since(started), state)
		if state.State == localv1.ConnectionState_CONNECTION_STATE_CONNECTED {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if state.GetConnection().GetExitCountryCode() != country || !state.ContributionEnabled {
		t.Fatalf("wrong connected capability/country: %v", state)
	}
	if crash {
		crashAndRecoverWindowsProduct(t, ctx, &client, country, wanProbe)
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, "example.com")
	if err != nil || len(addresses) == 0 {
		t.Fatalf("native resolver: %v %v", addresses, err)
	}
	t.Logf("native DNS passed: %d answers", len(addresses))
	pingCtx, stopPing := context.WithTimeout(ctx, 8*time.Second)
	output, err := exec.CommandContext(pingCtx, "ping.exe", "-4", "-n", "1", "-w", "3000", "1.1.1.1").CombinedOutput()
	stopPing()
	if err != nil {
		t.Fatalf("ICMP: %v: %s", err, output)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Transport: transport, Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		t.Fatal("direct no-proxy HTTPS probe:", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 128))
	closeErr := response.Body.Close()
	ip, parseErr := netip.ParseAddr(strings.TrimSpace(string(data)))
	if response.StatusCode != 200 || readErr != nil || closeErr != nil || parseErr != nil || !ip.IsGlobalUnicast() {
		t.Fatalf("HTTPS egress probe: status=%d body=%q read=%v close=%v parse=%v", response.StatusCode, data, readErr, closeErr, parseErr)
	}
	if ip != expectedEgress {
		t.Fatalf("wrong user traffic path: egress=%s expected=%s", ip, expectedEgress)
	}
	t.Logf("user traffic DNS/ICMP/HTTPS PASS; observed HTTPS egress=%s", ip)
	if os.Getenv("IRVIO_WINDOWS_PRODUCT_REBOOT") == "leave-connected-for-real-reboot" {
		options, err := resolveProductOptions(productOptions{role: productruntime.Desktop})
		if err != nil {
			t.Fatal(err)
		}
		intent, present, err := (productintent.Store{Path: options.intentPath}).Read()
		if err != nil || !present || !intent.ConnectIntent {
			t.Fatalf("connected intent not durable: %+v %v %v", intent, present, err)
		}
		leaveForReboot = true
		t.Log("CONNECTED session deliberately retained for the external real-reboot harness")
	}
}

func TestWindowsNativeProductPostReboot(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_PRODUCT_REBOOT") != "verify-real-reboot" {
		t.Skip("external real-reboot harness required")
	}
	requireWindowsProductFixture(t)
	manager, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Disconnect()
	for _, name := range []string{windowsProductService, windowsRecoveryService} {
		service, err := manager.OpenService(name)
		if err != nil {
			t.Fatal(err)
		}
		status, queryErr := service.Query()
		config, configErr := service.Config()
		_ = service.Close()
		neverStarted := name == windowsProductService && status.Win32ExitCode == uint32(windows.ERROR_SERVICE_NEVER_STARTED)
		if queryErr != nil || configErr != nil || status.State != svc.Stopped || (status.Win32ExitCode != 0 && !neverStarted) {
			t.Fatalf("post-boot %s status=%+v query=%v config=%v", name, status, queryErr, configErr)
		}
		if name == windowsProductService && config.StartType != mgr.StartManual {
			t.Fatal("desktop must remain demand-start")
		}
	}
	t.Setenv("IRVIO_WINDOWS_PRODUCT_IDLE_CLEANUP", "1")
	TestWindowsNativeProductIdleCleanup(t)
	wanProbe := os.Getenv("IRVIO_WINDOWS_PRODUCT_WAN_PROBE")
	if wanProbe == "" {
		t.Fatal("explicit ordinary WAN probe required")
	}
	conn, err := net.DialTimeout("tcp", wanProbe, 3*time.Second)
	if err != nil {
		t.Fatal("ordinary WAN after reboot:", err)
	}
	_ = conn.Close()
	service, err := manager.OpenService(windowsProductService)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var client *localapi.Client
	for ctx.Err() == nil {
		client, err = localapi.Dial(ctx, localapi.DefaultSocketPath)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if client == nil {
		t.Fatal("post-boot product did not start:", err)
	}
	defer client.Close()
	defer func() {
		if err := client.Shutdown(ctx); err != nil {
			t.Error("post-boot shutdown:", err)
		}
	}()
	for attempt := 0; attempt < 5; attempt++ {
		status, err := client.GetStatus(ctx)
		if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED || status.ContributionEnabled || status.PrivateHostingWanted || status.CountryCode != "" {
			t.Fatalf("post-boot product not IDLE: %v %v", status, err)
		}
		time.Sleep(time.Second)
	}
	bootstrap, err := client.GetBootstrapState(ctx)
	if err != nil || bootstrap.ConfiguredCount == 0 {
		t.Fatalf("durable bootstrap was lost: %v %v", bootstrap, err)
	}
	t.Log("post-boot demand service stayed stopped; WFP/intent absent; ordinary WAN works; manual start stays IDLE with bootstrap preserved")
}

func requireWindowsProductFixture(t *testing.T) {
	t.Helper()
	manager, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(windowsProductService)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	config, err := service.Config()
	if err != nil || config.BinaryPathName != `"C:\Program Files\IRVIO-W08\irviod.exe"` {
		t.Fatalf("not the disposable product fixture: %v %v", config, err)
	}
}

func crashAndRecoverWindowsProduct(t *testing.T, ctx context.Context, live **localapi.Client, country, wanProbe string) {
	t.Helper()
	old := *live
	manager, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(windowsProductService)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	config, err := service.Config()
	if err != nil || config.BinaryPathName != `"C:\Program Files\IRVIO-W08\irviod.exe"` {
		t.Fatalf("unexpected crash target: %v %v", config, err)
	}
	if err := service.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 8 * time.Second}}, 86400); err != nil {
		t.Fatal(err)
	}
	status, err := service.Query()
	if err != nil || status.State != svc.Running || status.ProcessId == 0 {
		t.Fatalf("missing running product: %v %v", status, err)
	}
	originalPID := status.ProcessId
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, originalPID)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	started := time.Now()
	if err := windows.TerminateProcess(handle, 1); err != nil {
		t.Fatal(err)
	}
	if wait, err := windows.WaitForSingleObject(handle, 3000); err != nil || wait != windows.WAIT_OBJECT_0 {
		t.Fatalf("crash did not complete: %d %v", wait, err)
	}
	_ = old.Close()
	store, err := wfp.NewStore(windowsProductOwner())
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.Inspect(ctx)
	if err != nil || !policy.Installed || !policy.Active || len(policy.Rules) == 0 {
		t.Fatalf("protection lost on process death: %+v %v", policy, err)
	}
	for attempt := 0; attempt < 4; attempt++ {
		conn, err := net.DialTimeout("tcp", wanProbe, 350*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Fatal("physical WAN leak after CONNECTED daemon crash")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("CONNECTED process killed pid=%d; persistent WFP rules=%d; 4 targeted WAN TCP probes blocked", originalPID, len(policy.Rules))
	var recovered *localapi.Client
	for ctx.Err() == nil {
		status, err = service.Query()
		if err == nil && status.State == svc.Running && status.ProcessId != originalPID {
			recovered, err = localapi.Dial(ctx, localapi.DefaultSocketPath)
			if err == nil {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if recovered == nil {
		t.Fatal("SCM product recovery did not become ready:", ctx.Err())
	}
	*live = recovered
	for {
		current, err := recovered.GetStatus(ctx)
		if err != nil {
			_ = recovered.Close()
			t.Fatal("recovered API:", err)
		}
		t.Logf("same-boot recovery elapsed=%s state=%v", time.Since(started), current)
		if current.State == localv1.ConnectionState_CONNECTION_STATE_CONNECTED {
			if current.GetConnection().GetExitCountryCode() != country {
				t.Fatal("country intent was not preserved")
			}
			break
		}
		if current.State == localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
			t.Fatal("same-boot CONNECT intent was lost")
		}
		select {
		case <-ctx.Done():
			t.Fatal("same-boot recovery:", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Logf("SCM restarted pid=%d; automatic CONNECT restored in %s", status.ProcessId, time.Since(started))
}

// This destructive fallback is confined to the stopped, explicitly named disposable VM installation.
func TestWindowsNativeProductDiscardLabSession(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_PRODUCT_DISCARD_LAB") != "discard-disposable-session" {
		t.Skip("explicit disposable session discard required")
	}
	manager, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(windowsProductService)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	config, err := service.Config()
	if err != nil {
		t.Fatal(err)
	}
	if config.BinaryPathName != `"C:\Program Files\IRVIO-W08\irviod.exe"` || config.StartType != mgr.StartDisabled {
		t.Fatal("not a disabled disposable product service")
	}
	status, err := service.Query()
	if err != nil || status.State != svc.Stopped {
		t.Fatalf("product not stopped: %v %v", status, err)
	}
	options, err := resolveProductOptions(productOptions{role: productruntime.Desktop})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(options.platform.root) != `C:\ProgramData\IRVIO` {
		t.Fatal("unexpected product root")
	}
	owner, err := acquireProductPlatform(options)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	store := productintent.Store{Path: options.intentPath}
	if err := store.BeginTeardown(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := cleanupWindowsProductProtection(ctx, options.platform); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
}
