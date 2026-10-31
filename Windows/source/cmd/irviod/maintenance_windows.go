// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/Irviodotnet/Irvio/internal/productprofile"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winfile"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Server maintenance accepts only the protected installer's exact bootstrap command.
func maintenanceServerCommand(root *winfile.Directory, base string) (string, error) {
	read := func(name string, limit int64) ([]byte, error) {
		f, err := root.OpenImmutableFile(name)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(f, limit+1))
		err = errors.Join(readErr, f.Close())
		if int64(len(data)) > limit {
			err = errors.Join(err, errors.New("oversized maintenance metadata"))
		}
		return data, err
	}
	raw, err := read("installation.json", 16*1024)
	if err != nil {
		return "", err
	}
	var record struct {
		Schema      int    `json:"schema"`
		Role        string `json:"role"`
		Manifest    string `json:"manifest_sha256"`
		Bootstrap   string `json:"bootstrap_sha256"`
		Development *bool  `json:"development_unsigned"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return "", errors.New("invalid server maintenance ownership record")
	}
	if decoder.Decode(new(any)) != io.EOF || record.Schema != 1 || record.Role != "server" || record.Development == nil {
		return "", errors.New("unsupported server maintenance ownership record")
	}
	for _, digest := range []string{record.Manifest, record.Bootstrap} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != digest {
			return "", errors.New("invalid server maintenance ownership digest")
		}
	}
	code, err := read("server-bootstrap.txt", productbootstrap.MaxBundleCodeBytes)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(code)
	if hex.EncodeToString(digest[:]) != record.Bootstrap {
		return "", errors.New("server maintenance bootstrap digest mismatch")
	}
	bundle, err := productbootstrap.Decode(string(code))
	if err != nil {
		return "", errors.New("invalid server maintenance bootstrap bundle")
	}
	canonical, err := productbootstrap.Encode(bundle)
	if err != nil || canonical != string(code) {
		return "", errors.New("noncanonical server maintenance bootstrap bundle")
	}
	return base + " --role server --bootstrap " + canonical, nil
}

func validateMaintenanceService(config mgr.Config, status svc.Status, command string) error {
	if config.BinaryPathName != command || config.ServiceStartName != "LocalSystem" || config.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS {
		return errors.New("maintenance service ownership differs from canonical installation")
	}
	if config.StartType != mgr.StartDisabled || status.State != svc.Stopped || status.ProcessId != 0 ||
		(status.Win32ExitCode != 0 && status.Win32ExitCode != 1077) {
		return errors.New("maintenance requires a cleanly stopped, disabled service")
	}
	return nil
}

func checkProductMaintenance(ctx context.Context) error {
	if err := inspectProductMaintenance(ctx, nil); err != nil {
		return err
	}
	fmt.Println("IRVIO_MAINTENANCE_CORE_CLEAN_V1")
	return nil
}

func removeProductMaintenanceProfile(ctx context.Context) error {
	if err := inspectProductMaintenance(ctx, func(paths productruntime.WindowsPaths) error {
		return productprofile.DeleteExistingContents(paths.Profile)
	}); err != nil {
		return err
	}
	fmt.Println("IRVIO_MAINTENANCE_PROFILE_REMOVED_V1")
	return nil
}

func maintenanceProgramDirectory(root *winfile.Directory, fallback string) (string, error) {
	f, err := root.OpenImmutableFile("installation.json")
	if errors.Is(err, os.ErrNotExist) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err = errors.Join(readErr, f.Close()); err != nil {
		return "", err
	}
	if len(raw) > 16*1024 {
		return "", errors.New("oversized installation record")
	}
	var record struct {
		Schema  int    `json:"schema"`
		Role    string `json:"role"`
		Program string `json:"program_directory"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}), &record); err != nil {
		return "", err
	}
	if record.Schema != 1 || (record.Role != "desktop" && record.Role != "server") {
		return "", errors.New("invalid installation ownership record")
	}
	if record.Program == "" {
		return fallback, nil
	}
	p := record.Program
	if record.Role != "desktop" || !filepath.IsAbs(p) || len(filepath.VolumeName(p)) != 2 || len(p) <= 3 || filepath.Clean(p) != p || strings.ContainsAny(p[2:], ":\"<>|?*\r\n\x00") {
		return "", errors.New("invalid installed program directory")
	}
	return p, nil
}

func inspectProductMaintenance(ctx context.Context, after func(productruntime.WindowsPaths) error) (result error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errors.New("elevated Windows installer required for maintenance inspection")
	}
	paths, err := productruntime.WindowsDefaultPaths()
	if err != nil {
		return err
	}
	root, err := winfile.OpenSystemDirectory(filepath.Dir(paths.Config))
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	program, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return err
	}
	program, err = maintenanceProgramDirectory(root, filepath.Join(program, "IRVIO"))
	if err != nil {
		return err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	command := `"` + filepath.Join(program, "daemon", "irviod.exe") + `"`
	for _, entry := range []struct{ name, command string }{{windowsProductService, command}, {windowsRecoveryService, command + " --boot-recover"}} {
		service, err := manager.OpenService(entry.name)
		if err != nil {
			return err
		}
		config, ce := service.Config()
		status, se := service.Query()
		closeErr := service.Close()
		if err := errors.Join(ce, se, closeErr); err != nil {
			return err
		}
		expected := entry.command
		if entry.name == windowsProductService && config.BinaryPathName != command {
			expected, err = maintenanceServerCommand(root, command)
			if err != nil {
				return fmt.Errorf("server maintenance: %w", err)
			}
		}
		if err := validateMaintenanceService(config, status, expected); err != nil {
			return fmt.Errorf("%s: %w", entry.name, err)
		}
	}
	finish := func() error {
		if err := inspectMaintenanceNetwork(ctx); err != nil {
			return err
		}
		if after != nil {
			return after(paths)
		}
		return nil
	}
	locked := false
	if err := winfile.InspectLockedDirectory(paths.Runtime, "product.lock", func(d *winfile.Directory) error {
		locked = true
		intent, err := d.OpenFile("session-state", false, false)
		if err == nil {
			return errors.Join(errors.New("session intent or teardown marker remains"), intent.Close())
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := irvioruntime.CheckWindowsDaemonJournalsInactive(ctx, filepath.Join(paths.Runtime, "network")); err != nil {
			return err
		}
		return finish()
	}); err != nil {
		return err
	}
	if !locked {
		return finish()
	}
	return nil
}

func inspectMaintenanceNetwork(ctx context.Context) error {
	store, err := wfp.NewStore(windowsProductOwner())
	if err != nil {
		return err
	}
	state, err := store.Inspect(ctx)
	if err != nil {
		return err
	}
	if state.Installed {
		return errors.New("persistent product WFP protection remains")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, iface := range interfaces {
		if strings.EqualFold(iface.Name, productTUNName()) {
			return errors.New("product tunnel adapter remains")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
