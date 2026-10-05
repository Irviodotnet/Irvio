// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"hydra.local/hydra/internal/exitfirewall"
	"hydra.local/hydra/internal/exitselector"
	"hydra.local/hydra/internal/killswitch"
	"hydra.local/hydra/internal/linuxroute"
	"hydra.local/hydra/internal/localapi"
	"hydra.local/hydra/internal/productbootstrap"
	"hydra.local/hydra/internal/productintent"
	"hydra.local/hydra/internal/productprofile"
	"hydra.local/hydra/internal/productruntime"
	"hydra.local/hydra/internal/rendezvous"
	irvioruntime "hydra.local/hydra/internal/runtime"
	"hydra.local/hydra/internal/trafficstats"
	"hydra.local/hydra/internal/tunnellease"
)

type productOptions struct {
	role              productruntime.Role
	dataDir           string
	bootstrap         []string
	apiSocket         string
	apiGroup          string
	allowTest         bool
	geoipDB           string
	publicProofTTL    time.Duration
	publicReverify    time.Duration
	publicRetry       time.Duration
	logger            *slog.Logger
	logStore          *localapi.LogStore
	inspectProtection func(context.Context) (bool, error)
	ensureProtection  func(context.Context) error
	cleanupProtection func(context.Context) error
	intentPath        string
	idleStopAfter     time.Duration
}

func runProduct(options productOptions) (runErr error) {
	root, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	return runProductWithContext(root, options)
}

func runProductWithContext(root context.Context, options productOptions) (runErr error) {
	if options.dataDir == "" {
		options.dataDir = productruntime.DefaultDataDir
	}
	if options.apiSocket == "" && options.role == productruntime.Desktop {
		options.apiSocket = localapi.DefaultSocketPath
	}
	if options.apiGroup == "" {
		options.apiGroup = "irvio"
	}
	profile, err := productprofile.Open(options.dataDir)
	if err != nil {
		return fmt.Errorf("open product profile: %w", err)
	}
	addresses, err := productruntime.ExpandBootstrap(options.bootstrap)
	if err != nil {
		return err
	}
	manager, err := productbootstrap.New(productbootstrap.Config{
		DataDir: options.dataDir, CLI: addresses, Logger: options.logger, AllowTestAddresses: options.allowTest,
	})
	if err != nil {
		return err
	}
	defer manager.Close()
	if options.role == productruntime.Server {
		return runProductServer(root, options, manager)
	}
	productCtx, cancelProduct := context.WithCancel(root)
	defer cancelProduct()
	controller := &desktopController{parent: productCtx, dataDir: options.dataDir,
		bootstrap: manager, logger: options.logger, allowTest: options.allowTest, geoipDB: options.geoipDB,
		intent: productintent.Store{Path: options.intentPath}, ensureProtection: options.ensureProtection,
		cleanupProtection: options.cleanupProtection}
	if controller.intent.Path == "" {
		controller.intent.Path = productintent.DefaultPath
	}
	if controller.ensureProtection == nil {
		controller.ensureProtection = ensureProductProtection
	}
	if controller.cleanupProtection == nil {
		controller.cleanupProtection = removeRecoveredProtection
	}
	inspect := options.inspectProtection
	if inspect == nil {
		inspect = inspectProductProtection
	}
	controller.recoveryPending, err = inspect(root)
	if err != nil {
		return fmt.Errorf("inspect same-boot protected session: %w", err)
	}
	state, present, err := controller.intent.Read()
	if err != nil {
		return fmt.Errorf("read same-boot ConnectIntent: %w", err)
	}
	if present && state.TeardownPending {
		if controller.recoveryPending {
			if err := controller.cleanupProtection(root); err != nil {
				return fmt.Errorf("finish interrupted Disconnect: %w", err)
			}
			controller.recoveryPending = false
		}
		if err := controller.intent.Clear(); err != nil {
			return err
		}
	} else if present && state.ConnectIntent {
		if err := controller.ensureProtection(root); err != nil {
			return fmt.Errorf("protect same-boot ConnectIntent: %w", err)
		}
		controller.recoveryPending = true
		controller.desiredCountry = exitselector.CountryIntent(state.Country)
	}
	cleanExit := false
	defer func() {
		if !cleanExit {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, controller.Shutdown(closeCtx))
	}()
	reset := newProfileResetController(controller)
	shutdown := &productShutdownController{controller: controller, committed: make(chan struct{})}
	server, err := newLocalAPIServer(options.apiSocket, options.apiGroup,
		controller, controller, options.logger, options.logStore, manager, reset, 45*time.Second, shutdown)
	if err != nil {
		return fmt.Errorf("start desktop Local API: %w", err)
	}
	if err := server.Start(productCtx); err != nil {
		return fmt.Errorf("start desktop Local API: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, server.Close()) }()
	options.logger.Info("PRODUCT_RUNTIME_READY", "role", "desktop", "state", controller.Status().State, "socket", options.apiSocket)
	go controller.recoveryLoop(productCtx)
	idleStopped := make(chan struct{})
	idleStopAfter := options.idleStopAfter
	if idleStopAfter == 0 {
		idleStopAfter = time.Minute
	}
	go watchProductIdle(productCtx, server, controller, idleStopAfter, idleStopped)
	select {
	case <-root.Done():
		cleanExit = true
		return nil
	case <-idleStopped:
		cleanExit = true
		return nil
	case <-shutdown.committed:
		cleanExit = true
		return nil
	case <-reset.Committed():
		if err := controller.Close(context.Background()); err != nil {
			return err
		}
		if err := server.Close(); err != nil {
			return err
		}
		if err := profile.DeleteContents(); err != nil {
			return err
		}
		return errProfileResetComplete
	}
}

func watchProductIdle(ctx context.Context, server *localapi.Server, controller *desktopController, delay time.Duration, stopped chan<- struct{}) {
	interval := min(5*time.Second, delay/2)
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var idleSince time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if server.SessionCount() != 0 || !controller.idleWithoutIntent() {
			idleSince = time.Time{}
			continue
		}
		if idleSince.IsZero() {
			idleSince = time.Now()
		} else if time.Since(idleSince) >= delay {
			close(stopped)
			return
		}
	}
}

func (d *desktopController) idleWithoutIntent() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.daemon == nil && !d.recoveryPending && d.desiredCountry == "" && !d.closed
}

func runProductServer(root context.Context, options productOptions, manager *productbootstrap.Manager) (runErr error) {
	addresses, err := manager.StartupAddresses()
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("server bootstrap bundle is empty or invalid: %w", err)
	}
	config, err := resolveProductDaemon(options, addresses, "")
	if err != nil {
		return err
	}
	daemon, err := irvioruntime.NewDaemon(config)
	if err != nil {
		return err
	}
	if err := daemon.Start(root); err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, daemon.Close(closeCtx))
	}()
	if err := manager.Attach(root, daemon.Node()); err != nil {
		return err
	}
	options.logger.Info("PRODUCT_RUNTIME_STARTED", "role", "server", "peer_id", daemon.Node().PeerID(),
		"readiness", "M11 and Rendezvous/Exit advertisement are asynchronous")
	<-root.Done()
	manager.Detach()
	return nil
}

type desktopController struct {
	traffic           atomic.Pointer[trafficstats.Session]
	parent            context.Context
	dataDir           string
	bootstrap         *productbootstrap.Manager
	logger            *slog.Logger
	allowTest         bool
	geoipDB           string
	intent            productintent.Store
	ensureProtection  func(context.Context) error
	cleanupProtection func(context.Context) error

	mu              sync.Mutex
	discoveryMu     sync.Mutex
	discoveryCancel context.CancelFunc
	discoveryDone   chan struct{}
	daemon          *irvioruntime.Daemon
	cancel          context.CancelFunc
	closed          bool
	recoveryPending bool
	desiredCountry  exitselector.CountryIntent
	countries       rendezvous.AvailableCountries
	countriesAt     time.Time
}

// TrafficStats never takes the Connect/Disconnect mutex or starts discovery.
func (d *desktopController) TrafficStats() trafficstats.Snapshot {
	if session := d.traffic.Load(); session != nil {
		return session.Snapshot()
	}
	return trafficstats.Snapshot{ClientsValid: true, MyValid: true, DownloadValid: true, SharedValid: true, Complete: true}
}

func (d *desktopController) Status() irvioruntime.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.daemon != nil {
		return d.daemon.Status()
	}
	if d.recoveryPending {
		state := irvioruntime.StateNoExit
		if d.desiredCountry != "" {
			state = irvioruntime.StateExitReselection
		}
		return irvioruntime.Status{State: state, Country: d.desiredCountry, ProtectedMode: killswitch.StateNoExit,
			ProtectionAdopted: true, UpdatedAt: time.Now().UTC()}
	}
	return irvioruntime.Status{State: irvioruntime.StateIdle, ProtectedMode: killswitch.StateVPNOff, UpdatedAt: time.Now().UTC()}
}

func (d *desktopController) AvailableCountries(ctx context.Context, refresh bool) (rendezvous.AvailableCountries, error) {
	d.discoveryMu.Lock()
	if d.discoveryDone != nil {
		select {
		case <-d.discoveryDone:
		default:
			d.discoveryMu.Unlock()
			return rendezvous.AvailableCountries{}, fmt.Errorf("country discovery already in progress")
		}
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		d.discoveryMu.Unlock()
		return rendezvous.AvailableCountries{}, irvioruntime.ErrClosed
	}
	ctx, cancel := context.WithCancel(ctx)
	d.discoveryCancel = cancel
	done := make(chan struct{})
	d.discoveryDone = done
	d.mu.Unlock()
	d.discoveryMu.Unlock()
	defer close(done)
	defer cancel()
	if d.daemon != nil {
		return d.daemon.AvailableCountries(ctx, refresh)
	}
	if !d.countriesAt.IsZero() && time.Since(d.countriesAt) < 10*time.Second {
		return d.countries, nil
	}
	addresses, err := d.bootstrap.StartupAddresses()
	if err != nil || len(addresses) == 0 {
		return rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesUnavailable, GeneratedAt: time.Now().UTC()}, nil
	}
	probe, err := irvioruntime.NewDaemon(irvioruntime.DaemonConfig{
		DataDir: d.dataDir, Bootstrap: addresses, DHTMode: "auto", Logger: d.logger,
		M12: irvioruntime.M12Config{Enabled: true, Policy: rendezvous.DefaultConfig()},
	})
	if err != nil {
		return rendezvous.AvailableCountries{}, err
	}
	if err := probe.Start(ctx); err != nil {
		if ctx.Err() != nil {
			return rendezvous.AvailableCountries{}, ctx.Err()
		}
		d.logger.Warn("PRODUCT_IDLE_COUNTRY_DISCOVERY_UNAVAILABLE", "reason", err)
		d.countries = rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesUnavailable, GeneratedAt: time.Now().UTC()}
		d.countriesAt = time.Now()
		return d.countries, nil
	}
	available, queryErr := probe.AvailableCountries(ctx, true)
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	closeErr := probe.Close(closeCtx)
	cancel()
	if closeErr != nil {
		return rendezvous.AvailableCountries{}, fmt.Errorf("close bounded idle country discovery: %w", closeErr)
	}
	if queryErr != nil {
		if ctx.Err() != nil {
			return rendezvous.AvailableCountries{}, ctx.Err()
		}
		d.logger.Warn("PRODUCT_IDLE_COUNTRY_DISCOVERY_UNAVAILABLE", "reason", queryErr)
		d.countries = rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesUnavailable, GeneratedAt: time.Now().UTC()}
		d.countriesAt = time.Now()
		return d.countries, nil
	}
	d.countries, d.countriesAt = available, time.Now()
	return available, nil
}

func (d *desktopController) Connect(ctx context.Context, country exitselector.CountryIntent) error {
	d.lockCommand()
	defer d.mu.Unlock()
	return d.connectLocked(ctx, country, true)
}

func (d *desktopController) connectLocked(ctx context.Context, country exitselector.CountryIntent, persist bool) error {
	if d.closed || d.daemon != nil {
		return irvioruntime.ErrInvalidState
	}
	addresses, err := d.bootstrap.StartupAddresses()
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("import a IRVIO bootstrap bundle before CONNECT: %w", irvioruntime.ErrInvalidConfig)
	}
	config, err := resolveProductDaemon(productOptions{role: productruntime.Desktop,
		dataDir: d.dataDir, logger: d.logger, allowTest: d.allowTest, geoipDB: d.geoipDB}, addresses, country)
	if err != nil {
		return err
	}
	daemon, err := irvioruntime.NewDaemon(config)
	if err != nil {
		return err
	}
	if err := d.ensureProtection(ctx); err != nil {
		return fmt.Errorf("protect ConnectIntent: %w", err)
	}
	d.recoveryPending = true
	if persist {
		if err := d.intent.Want(string(country)); err != nil {
			return fmt.Errorf("persist ConnectIntent: %w", err)
		}
		d.desiredCountry = country
	}
	runCtx, cancel := context.WithCancel(d.parent)
	stopRequest := context.AfterFunc(ctx, cancel)
	if err := daemon.Start(runCtx); err != nil {
		stopRequest()
		cancel()
		return errors.Join(err, d.ensureProtection(context.Background()))
	}
	if err := ctx.Err(); err != nil {
		stopRequest()
		cancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		return errors.Join(err, daemon.Close(closeCtx), d.ensureProtection(context.Background()))
	}
	stopRequest()
	if err := d.bootstrap.Attach(runCtx, daemon.Node()); err != nil {
		cancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		return errors.Join(err, daemon.Close(closeCtx), d.ensureProtection(context.Background()))
	}
	d.daemon, d.cancel = daemon, cancel
	d.traffic.Store(trafficstats.Start(uint64(time.Now().UnixNano()), persist, daemon.TrafficSource()))
	d.recoveryPending = false
	d.logger.Info("PRODUCT_DESKTOP_NETWORK_JOINED", "role", "client+exit")
	return nil
}

func (d *desktopController) Disconnect(ctx context.Context) error {
	d.lockCommand()
	defer d.mu.Unlock()
	if err := d.intent.BeginTeardown(); err != nil {
		return fmt.Errorf("persist Disconnect intent: %w", err)
	}
	d.desiredCountry = ""
	if d.daemon == nil {
		if d.recoveryPending {
			if err := d.cleanupProtection(ctx); err != nil {
				return err
			}
			d.recoveryPending = false
			d.desiredCountry = ""
			if err := d.intent.Clear(); err != nil {
				return err
			}
			d.logger.Info("PRODUCT_DESKTOP_IDLE", "protected_mode", "off", "network_participation", false)
			return nil
		}
		d.desiredCountry = ""
		return d.intent.Clear()
	}
	d.bootstrap.Detach()
	err := d.daemon.Close(ctx)
	if err != nil {
		return err
	}
	if session := d.traffic.Load(); session != nil {
		session.Stop()
	}
	d.cancel()
	d.daemon, d.cancel = nil, nil
	d.recoveryPending = false
	d.desiredCountry = ""
	if err := d.intent.Clear(); err != nil {
		return err
	}
	d.logger.Info("PRODUCT_DESKTOP_IDLE", "protected_mode", "off", "network_participation", false)
	return nil
}

func (d *desktopController) Shutdown(ctx context.Context) error {
	if err := d.Disconnect(ctx); err != nil {
		return err
	}
	return d.Close(ctx)
}

func (d *desktopController) recoveryLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		d.mu.Lock()
		country := d.desiredCountry
		pending := country != "" && d.daemon == nil && !d.closed
		d.mu.Unlock()
		if pending {
			d.lockCommand()
			if d.desiredCountry != country || d.daemon != nil || d.closed {
				d.mu.Unlock()
				timer.Reset(10 * time.Second)
				continue
			}
			attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
			err := d.connectLocked(attempt, country, false)
			cancel()
			if err != nil && ctx.Err() == nil {
				d.logger.Warn("PRODUCT_SAME_BOOT_RECOVERY_RETRY", "reason", err)
			}
			d.mu.Unlock()
		}
		timer.Reset(10 * time.Second)
	}
}

type productShutdownController struct {
	controller *desktopController
	committed  chan struct{}
	once       sync.Once
}

func (s *productShutdownController) PrepareShutdown(ctx context.Context) error {
	return s.controller.Shutdown(ctx)
}

func (s *productShutdownController) CommitShutdown() { s.once.Do(func() { close(s.committed) }) }

func (d *desktopController) Close(ctx context.Context) error {
	d.lockCommand()
	defer d.mu.Unlock()
	d.closed = true
	if d.daemon == nil {
		return nil
	}
	d.bootstrap.Detach()
	err := d.daemon.Close(ctx)
	if err == nil {
		if session := d.traffic.Load(); session != nil {
			session.Stop()
		}
		d.cancel()
		d.daemon, d.cancel = nil, nil
	}
	return err
}

func (d *desktopController) lockCommand() {
	d.discoveryMu.Lock()
	if d.discoveryCancel != nil {
		d.discoveryCancel()
		<-d.discoveryDone
	}
	d.mu.Lock()
	d.discoveryMu.Unlock()
}

func productProtectionPolicy() killswitch.Policy {
	return killswitch.Policy{TUNName: "hydra0", BypassMark: linuxroute.IrvioBypassMark,
		TunnelControlMark: linuxroute.IrvioTunnelControlMark}
}

func inspectProductProtection(ctx context.Context) (bool, error) {
	backend := killswitch.NFTBackend{}
	snapshot, err := backend.Inspect(ctx, productProtectionPolicy())
	if err != nil || !snapshot.Installed {
		return false, err
	}
	if _, _, err := backend.Ensure(ctx, productProtectionPolicy()); err != nil {
		return false, err
	}
	snapshot, err = backend.Inspect(ctx, productProtectionPolicy())
	if err != nil {
		return false, err
	}
	if !snapshot.Complete {
		return false, fmt.Errorf("persistent protected state is incomplete")
	}
	return true, nil
}

func ensureProductProtection(ctx context.Context) error {
	backend := killswitch.NFTBackend{}
	if _, _, err := backend.Ensure(ctx, productProtectionPolicy()); err != nil {
		return err
	}
	snapshot, err := backend.Inspect(ctx, productProtectionPolicy())
	if err != nil {
		return err
	}
	if !snapshot.Installed || !snapshot.Complete {
		return fmt.Errorf("IRVIO ProtectedMode is incomplete")
	}
	return nil
}

const productBootMarker = "/var/lib/irvio/boot-id"

func recoverPreviousBoot(ctx context.Context) error {
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return fmt.Errorf("read current boot ID: %w", err)
	}
	current := strings.TrimSpace(string(bootID))
	if len(current) < 32 || len(current) > 64 {
		return fmt.Errorf("current boot ID is empty")
	}
	parent := filepath.Dir(productBootMarker)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 ||
		parentInfo.Mode().Perm()&0o022 != 0 || !ok || parentStat.Uid != 0 {
		return fmt.Errorf("unsafe IRVIO state directory")
	}
	if info, err := os.Lstat(productBootMarker); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Uid != 0 {
			return fmt.Errorf("unsafe IRVIO boot marker")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	previous, err := os.ReadFile(productBootMarker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read IRVIO boot marker: %w", err)
	}
	if strings.TrimSpace(string(previous)) == current {
		return nil
	}
	if len(previous) != 0 {
		if err := removeRecoveredProtection(ctx); err != nil {
			return fmt.Errorf("reconcile previous-boot IRVIO state: %w", err)
		}
	}
	file, err := os.CreateTemp(parent, ".boot-id-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.WriteString(current + "\n"); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), productBootMarker)
}

func removeRecoveredProtection(ctx context.Context) error {
	if _, err := os.Stat("/sys/class/net/hydra0"); err == nil {
		return fmt.Errorf("cannot leave recovered protection while hydra0 is still owned by a live process")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect recovered TUN ownership: %w", err)
	}
	if err := linuxroute.CleanupCrashResidue(); err != nil {
		return fmt.Errorf("remove crash-owned IRVIO routing: %w", err)
	}
	if err := exitfirewall.CleanupCrashResidue(ctx); err != nil {
		return fmt.Errorf("remove crash-owned Exit firewall: %w", err)
	}
	controller, err := killswitch.New(killswitch.Config{Backend: killswitch.NFTBackend{}, Policy: productProtectionPolicy()})
	if err != nil {
		return err
	}
	if _, err := controller.Protect(ctx); err != nil {
		return err
	}
	if err := controller.Disconnect(ctx); err != nil {
		return err
	}
	return controller.Close(ctx)
}

func resolveProductDaemon(options productOptions, addresses []string, country exitselector.CountryIntent) (irvioruntime.DaemonConfig, error) {
	pool := productruntime.BootstrapSTUNIPv4WithTestAddresses(addresses, options.allowTest)
	if len(pool) == 0 {
		return irvioruntime.DaemonConfig{}, fmt.Errorf("bootstrap bundle has no direct IPv4 server for STUN UDP/3478")
	}
	path, err := productruntime.ResolveBootstrapUnderlay(pool, linuxroute.IrvioBypassMark)
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	exitPool, tunAddress, err := productruntime.ResolveExitPool(options.dataDir)
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	bypass, err := productruntime.ConnectedBypassPrefixes()
	if err != nil {
		return irvioruntime.DaemonConfig{}, err
	}
	targets := make(irvioruntime.StaticTargets, 0, len(addresses))
	for _, address := range addresses {
		target, parseErr := parseUnderlayTarget(address)
		if parseErr == nil {
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return irvioruntime.DaemonConfig{}, fmt.Errorf("bootstrap bundle contains no usable underlay target")
	}
	production := &irvioruntime.ProductionConfig{
		STUNIPv4Pool: pool, TUNName: "hydra0",
		TunnelDNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		Bypass:           bypass, Obfuscation: irvioruntime.DefaultProductionObfuscation(), ConnectCountry: country,
		ExitWANName: path.IfName, ExitTUNAddresses: []netip.Prefix{tunAddress}, ExitIPv4Pool: exitPool,
		ExitManagement: bypass, LeaseTTL: tunnellease.DefaultTTL, LeaseSweep: time.Second,
	}
	server := options.role == productruntime.Server
	proofTTL, reverifyLead, retryDelay := 5*time.Minute, time.Minute, time.Minute
	if options.publicProofTTL != 0 {
		proofTTL = options.publicProofTTL
	}
	if options.publicReverify != 0 {
		reverifyLead = options.publicReverify
	}
	if options.publicRetry != 0 {
		retryDelay = options.publicRetry
	}
	geoipDB := productruntime.DefaultGeoIPPath
	if options.geoipDB != "" {
		geoipDB = options.geoipDB
	}
	listenPort := 0
	if server {
		listenPort = 4000
		production.UDPPort = productruntime.ServerUDPPort
		production.RequireFixedUDPPort = true
	} else {
		listenPort, err = productruntime.ResolveListenPort(options.dataDir)
		if err != nil {
			return irvioruntime.DaemonConfig{}, err
		}
	}
	config := irvioruntime.DaemonConfig{
		DataDir: options.dataDir, ListenPort: listenPort, Bootstrap: addresses,
		Exit: true, DHTMode: "server", Logger: options.logger, ProtectedMode: !server, ProtectAtStart: !server,
		Production: production, UnderlayTargets: targets,
		AutoExitCountry: true, RequireExitCountry: !server,
		PublicSTUN: server, PublicSTUNBind: path.Source,
		PublicRole: irvioruntime.PublicRoleConfig{Requested: server, DiscoverVerifiers: server,
			ProofTTL: proofTTL, ReverifyLead: reverifyLead, RetryDelay: retryDelay,
			OperationTimeout: 10 * time.Second, Logger: options.logger},
		M12: irvioruntime.M12Config{Enabled: true, Serve: server,
			GeoIPDatabase: geoipDB, SessionControlPort: 51822,
			Policy: rendezvous.DefaultConfig()},
	}
	if options.allowTest {
		config.PublicRole.AllowTestAddresses = true
		config.M12.AllowTestAddresses = true
	}
	return config, nil
}

var _ localapi.RuntimeController = (*desktopController)(nil)
var _ localapi.AvailableCountriesController = (*desktopController)(nil)
