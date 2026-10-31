// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/Irviodotnet/Irvio/internal/productintent"
	"github.com/Irviodotnet/Irvio/internal/productprofile"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/trafficstats"
	"github.com/libp2p/go-libp2p/core/peer"
)

type productOptions struct {
	platform          productPlatformOptions
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
	ready             func()
}

func runProductWithContext(root context.Context, options productOptions) (runErr error) {
	options, err := resolveProductOptions(options)
	if err != nil {
		return err
	}
	platform, err := acquireProductPlatform(options)
	if err != nil {
		return err
	}
	defer func() {
		if err := platform.Close(); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
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
	controller := &desktopController{parent: productCtx, dataDir: options.dataDir, platform: options.platform,
		bootstrap: manager, logger: options.logger, allowTest: options.allowTest, geoipDB: options.geoipDB,
		intent: productintent.Store{Path: options.intentPath}, ensureProtection: options.ensureProtection,
		cleanupProtection: options.cleanupProtection}
	if err := configureProductDesktop(root, options, controller); err != nil {
		return err
	}
	state, present, err := readProductIntent(root, controller)
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
	} else if present {
		if err := controller.restoreIntent(root, state); err != nil {
			return err
		}
	}
	if !present || state.TeardownPending {
		enabled, err := controller.persistentHosting()
		if err != nil {
			return err
		}
		if enabled {
			if err := controller.intent.WantPrivate("", true); err != nil {
				return err
			}
			if err := controller.restoreIntent(root, productintent.State{Version: 2, PrivateHosting: true}); err != nil {
				return err
			}
		}
	}
	cleanExit := false
	defer func() {
		if !cleanExit {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := controller.disconnect(closeCtx, false)
		if err == nil {
			err = controller.Close(closeCtx)
		}
		runErr = errors.Join(runErr, err)
	}()
	reset := newProfileResetController(controller)
	reset.validate = profile.ValidateDeletion
	shutdown := &productShutdownController{controller: controller, committed: make(chan struct{})}
	server, err := newLocalAPIServer(options.apiSocket, options.apiGroup,
		controller, controller, options.logger, options.logStore, manager, reset, 45*time.Second, shutdown)
	if err != nil {
		return fmt.Errorf("start desktop Local API: %w", err)
	}
	if err := server.Start(productCtx); err != nil {
		return fmt.Errorf("start desktop Local API: %w", err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	options.logger.Info("PRODUCT_RUNTIME_READY", "role", "desktop", "state", controller.Status().State, "socket", options.apiSocket)
	if options.ready != nil {
		options.ready()
	}
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
	return d.daemon == nil && d.idleProbe == nil && !d.recoveryPending && !d.teardownPending && d.desiredCountry == "" && !d.desiredPrivate.active() && !d.closed
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
	if options.ready != nil {
		options.ready()
	}
	<-root.Done()
	manager.Detach()
	return nil
}

type desktopDaemon interface {
	Start(context.Context) error
	Close(context.Context) error
	Node() *discovery.Node
	Status() irvioruntime.Status
	AvailableCountries(context.Context, bool) (rendezvous.AvailableCountries, error)
	TrafficSource() trafficstats.Source
	PrivateConnectedPeers() map[peer.ID]bool
	RevokePrivateMember(context.Context, peer.ID) error
}

var _ desktopDaemon = (*irvioruntime.Daemon)(nil)

type desktopController struct {
	platform          productPlatformOptions
	traffic           atomic.Pointer[trafficstats.Session]
	statusCache       atomic.Pointer[irvioruntime.Status]
	parent            context.Context
	dataDir           string
	bootstrap         *productbootstrap.Manager
	logger            *slog.Logger
	allowTest         bool
	geoipDB           string
	intent            productintent.Store
	ensureProtection  func(context.Context) error
	cleanupProtection func(context.Context) error

	mu                 sync.Mutex
	discoveryMu        sync.Mutex
	discoveryCancel    context.CancelFunc
	discoveryDone      chan struct{}
	daemon             desktopDaemon
	idleProbe          desktopDaemon
	idleCleanupPending bool
	cancel             context.CancelFunc
	startupRollback    bool
	rollbackProtected  bool
	closed             bool
	recoveryPending    bool
	teardownPending    bool
	desiredCountry     exitselector.CountryIntent
	desiredPrivate     privateProductIntent
	privateStore       *privateaccess.Store
	countries          rendezvous.AvailableCountries
	countriesAt        time.Time
	countryView        countryPresenceView
	warmDiscovery      *rendezvous.WarmDiscovery
	warmBootstrap      []string
}

// TrafficStats never takes the Connect/Disconnect mutex or starts discovery.
func (d *desktopController) TrafficStats() trafficstats.Snapshot {
	if session := d.traffic.Load(); session != nil {
		return session.Snapshot()
	}
	return trafficstats.Snapshot{ClientsValid: true, MyValid: true, DownloadValid: true, SharedValid: true, Complete: true}
}

func (d *desktopController) Status() irvioruntime.Status {
	if !d.mu.TryLock() {
		if cached := d.statusCache.Load(); cached != nil {
			return *cached
		}
		return irvioruntime.Status{State: irvioruntime.StateStarting, UpdatedAt: time.Now().UTC()}
	}
	defer d.mu.Unlock()
	status := d.statusLocked()
	d.cacheStatus(status)
	return status
}

func (d *desktopController) cacheStatus(status irvioruntime.Status) {
	d.statusCache.Store(&status)
}

func (d *desktopController) statusLocked() (status irvioruntime.Status) {
	defer func() {
		status.PrivateHostingWanted = d.desiredPrivate.Hosting
		if d.desiredPrivate.Target != "" {
			status.Target = d.intentTarget()
		}
	}()
	if d.teardownPending || d.idleCleanupPending {
		return irvioruntime.Status{State: irvioruntime.StateDisconnecting, UpdatedAt: time.Now().UTC()}
	}
	if d.daemon != nil && !d.startupRollback {
		return d.daemon.Status()
	}
	if d.recoveryPending {
		state := irvioruntime.StateNoExit
		if d.desiredCountry != "" || d.desiredPrivate.Target != "" {
			state = irvioruntime.StateExitReselection
		}
		return irvioruntime.Status{State: state, Country: d.desiredCountry, Target: d.intentTarget(), ProtectedMode: killswitch.StateNoExit,
			ProtectionAdopted: true, UpdatedAt: time.Now().UTC()}
	}
	if d.desiredPrivate.Hosting {
		return irvioruntime.Status{State: irvioruntime.StateStarting, ProtectedMode: killswitch.StateVPNOff, UpdatedAt: time.Now().UTC()}
	}
	return irvioruntime.Status{State: irvioruntime.StateIdle, ProtectedMode: killswitch.StateVPNOff, UpdatedAt: time.Now().UTC()}
}

func (d *desktopController) AvailableCountries(ctx context.Context, refresh bool) (result rendezvous.AvailableCountries, resultErr error) {
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
	if err := d.closeIdleProbeLocked(ctx); err != nil {
		d.mu.Unlock()
		d.discoveryMu.Unlock()
		return rendezvous.AvailableCountries{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	d.discoveryCancel = cancel
	done := make(chan struct{})
	d.discoveryDone = done
	d.mu.Unlock()
	d.discoveryMu.Unlock()
	defer close(done)
	defer cancel()
	defer func() {
		if resultErr == nil && ctx.Err() == nil {
			result = d.countryView.observe(result, time.Now())
		}
	}()
	if d.startupRollback {
		return rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesUnavailable, GeneratedAt: time.Now().UTC()}, nil
	}
	if d.daemon != nil {
		return d.daemon.AvailableCountries(ctx, refresh)
	}
	if !d.countriesAt.IsZero() && time.Since(d.countriesAt) < 10*time.Second {
		return d.countries, nil
	}
	addresses, err := d.bootstrap.StartupAddresses()
	if err != nil || len(addresses) == 0 {
		d.countryView = countryPresenceView{}
		return rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesUnavailable, GeneratedAt: time.Now().UTC()}, nil
	}
	probe, err := irvioruntime.NewDaemon(irvioruntime.DaemonConfig{
		Platform: productDaemonPlatform(d.platform),
		DataDir:  d.dataDir, Bootstrap: addresses, DHTMode: "auto", Logger: d.logger,
		M12: irvioruntime.M12Config{Enabled: true, Policy: rendezvous.DefaultConfig()},
	})
	if err != nil {
		return rendezvous.AvailableCountries{}, err
	}
	return d.queryIdleCountries(ctx, probe, addresses...)
}

// discoveryDone fences the whole query; its resource owner also survives a failed Close.
func (d *desktopController) queryIdleCountries(ctx context.Context, probe desktopDaemon, addresses ...string) (rendezvous.AvailableCountries, error) {
	d.mu.Lock()
	if d.idleProbe != nil {
		d.mu.Unlock()
		return rendezvous.AvailableCountries{}, irvioruntime.ErrInvalidState
	}
	d.idleProbe = probe
	d.mu.Unlock()
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var available rendezvous.AvailableCountries
	queryErr := probe.Start(probeCtx)
	if queryErr == nil {
		available, queryErr = probe.AvailableCountries(probeCtx, true)
	}
	var warm *rendezvous.WarmDiscovery
	if source, ok := probe.(interface {
		WarmDiscovery() *rendezvous.WarmDiscovery
	}); ok && queryErr == nil && ctx.Err() == nil {
		warm = source.WarmDiscovery()
	}
	cancel()
	d.mu.Lock()
	closeErr := d.closeIdleProbeLocked(context.Background())
	d.mu.Unlock()
	if closeErr != nil {
		return rendezvous.AvailableCountries{}, errors.Join(queryErr, closeErr)
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
	d.warmDiscovery, d.warmBootstrap = warm, append([]string(nil), addresses...)
	return available, nil
}

func (d *desktopController) closeIdleProbeLocked(ctx context.Context) error {
	if d.idleProbe == nil {
		return nil
	}
	closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := d.idleProbe.Close(closeCtx); err != nil {
		d.idleCleanupPending = true
		return fmt.Errorf("close bounded idle country discovery: %w", err)
	}
	d.idleProbe, d.idleCleanupPending = nil, false
	return nil
}

func (d *desktopController) Connect(ctx context.Context, country exitselector.CountryIntent) error {
	d.lockCommand()
	defer d.mu.Unlock()
	return d.connectLocked(ctx, country, true)
}

func (d *desktopController) connectLocked(ctx context.Context, country exitselector.CountryIntent, persist bool) error {
	if d.closed || d.daemon != nil || d.teardownPending || d.desiredPrivate.active() {
		return irvioruntime.ErrInvalidState
	}
	if err := d.closeIdleProbeLocked(ctx); err != nil {
		return err
	}
	starting := d.statusLocked()
	starting.State, starting.Country = irvioruntime.StateStarting, country
	starting.Target = exitselector.Target{Kind: exitselector.TargetPublicCountry, Country: country}
	d.cacheStatus(starting)
	defer func() { d.cacheStatus(d.statusLocked()) }()
	addresses, err := d.bootstrap.StartupAddresses()
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("import a IRVIO bootstrap bundle before CONNECT: %w", irvioruntime.ErrInvalidConfig)
	}
	config, err := resolveProductDaemon(productOptions{role: productruntime.Desktop,
		dataDir: d.dataDir, logger: d.logger, allowTest: d.allowTest, geoipDB: d.geoipDB, platform: d.platform}, addresses, country)
	if err != nil {
		return err
	}
	d.applyWarmDiscovery(&config)
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
	return d.startDaemonLocked(ctx, daemon, persist, true)
}

func (d *desktopController) startDaemonLocked(ctx context.Context, daemon desktopDaemon, persist, protected bool) error {
	if d.daemon != nil || daemon == nil {
		return irvioruntime.ErrInvalidState
	}
	runCtx, cancel := context.WithCancel(d.parent)
	d.daemon, d.cancel = daemon, cancel
	d.startupRollback, d.rollbackProtected = true, protected
	stopRequest := context.AfterFunc(ctx, cancel)
	rollback := func(cause error) error {
		d.logger.Warn("PRODUCT_DESKTOP_START_FAILED", "reason", cause)
		stopRequest()
		cancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer closeCancel()
		return errors.Join(cause, d.rollbackStartupLocked(closeCtx))
	}
	if err := daemon.Start(runCtx); err != nil {
		return rollback(err)
	}
	stopRequest()
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if err := d.bootstrap.Attach(runCtx, daemon.Node()); err != nil {
		return rollback(err)
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	d.startupRollback, d.rollbackProtected = false, false
	d.traffic.Store(trafficstats.Start(uint64(time.Now().UnixNano()), persist, daemon.TrafficSource()))
	d.recoveryPending = false
	d.logger.Info("PRODUCT_DESKTOP_NETWORK_JOINED", "private", d.desiredPrivate.active(), "upstream_protected", protected)
	return nil
}

// Failed startup retains its daemon until cleanup succeeds; a replacement cannot overlap its resources.
func (d *desktopController) rollbackStartupLocked(ctx context.Context) error {
	if !d.startupRollback {
		return nil
	}
	d.bootstrap.Detach()
	protected := d.rollbackProtected
	err := d.daemon.Close(ctx)
	if err == nil {
		d.releaseDaemonLocked()
	}
	if protected {
		protectCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = errors.Join(err, d.ensureProtection(protectCtx))
	}
	return err
}

func (d *desktopController) releaseDaemonLocked() {
	d.cancel()
	d.daemon, d.cancel = nil, nil
	d.startupRollback, d.rollbackProtected = false, false
}

func (d *desktopController) Disconnect(ctx context.Context) (result error) {
	return d.disconnect(ctx, true)
}

func (d *desktopController) disconnect(ctx context.Context, clearPersistent bool) (result error) {
	d.lockCommand()
	defer d.mu.Unlock()
	if clearPersistent {
		if err := d.clearPersistentHosting(); err != nil {
			return err
		}
	}
	defer func() {
		if result != nil && d.logger != nil {
			d.logger.Warn("PRODUCT_DESKTOP_DISCONNECT_FAILED", "reason", result)
		}
	}()
	stopping := d.statusLocked()
	stopping.State = irvioruntime.StateDisconnecting
	d.cacheStatus(stopping)
	defer func() { d.cacheStatus(d.statusLocked()) }()
	if err := d.intent.BeginTeardown(); err != nil {
		return fmt.Errorf("persist Disconnect intent: %w", err)
	}
	d.teardownPending = true
	d.desiredCountry = ""
	d.desiredPrivate = privateProductIntent{}
	if err := d.closeIdleProbeLocked(ctx); err != nil {
		return err
	}
	if d.daemon == nil {
		if d.recoveryPending {
			if err := d.cleanupProtection(ctx); err != nil {
				return err
			}
			d.recoveryPending = false
			d.desiredCountry = ""
			if err := d.finishTeardownLocked(); err != nil {
				return err
			}
			d.logger.Info("PRODUCT_DESKTOP_IDLE", "protected_mode", "off", "network_participation", false)
			return nil
		}
		d.desiredCountry = ""
		return d.finishTeardownLocked()
	}
	d.bootstrap.Detach()
	err := d.daemon.Close(ctx)
	if err != nil {
		return err
	}
	if session := d.traffic.Load(); session != nil {
		session.Stop()
	}
	d.releaseDaemonLocked()
	d.recoveryPending = false
	d.desiredCountry = ""
	if err := d.finishTeardownLocked(); err != nil {
		return err
	}
	d.logger.Info("PRODUCT_DESKTOP_IDLE", "protected_mode", "off", "network_participation", false)
	return nil
}

func (d *desktopController) DisconnectRetryNeeded() bool {
	if !d.mu.TryLock() {
		return false
	}
	defer d.mu.Unlock()
	return d.teardownPending
}

func (d *desktopController) finishTeardownLocked() error {
	if err := finishProductPlatformTeardown(d); err != nil {
		return err
	}
	if err := d.intent.Clear(); err != nil {
		return err
	}
	d.teardownPending = false
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
		private := d.desiredPrivate
		pending := (d.idleCleanupPending || (country != "" || private.active()) && (d.daemon == nil || d.startupRollback)) && !d.closed && !d.teardownPending
		d.mu.Unlock()
		if pending {
			d.lockCommand()
			if d.desiredCountry != country || d.desiredPrivate != private || d.daemon != nil && !d.startupRollback || d.closed || d.teardownPending {
				d.mu.Unlock()
				timer.Reset(10 * time.Second)
				continue
			}
			attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
			err := d.closeIdleProbeLocked(attempt)
			if err == nil {
				err = d.rollbackStartupLocked(attempt)
			}
			if err != nil {
				d.logger.Warn("PRODUCT_STARTUP_ROLLBACK_RETRY", "reason", err)
			} else if private.active() {
				err = d.connectPrivateLocked(attempt, private, false)
			} else if country != "" {
				err = d.connectLocked(attempt, country, false)
			}
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
	if err := d.closeIdleProbeLocked(ctx); err != nil {
		return err
	}
	if d.daemon == nil {
		return d.closePrivateStoreLocked()
	}
	d.bootstrap.Detach()
	err := d.daemon.Close(ctx)
	if err == nil {
		if session := d.traffic.Load(); session != nil {
			session.Stop()
		}
		d.releaseDaemonLocked()
		err = d.closePrivateStoreLocked()
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
	d.cacheStatus(d.statusLocked())
	d.discoveryMu.Unlock()
}

var _ localapi.RuntimeController = (*desktopController)(nil)
var _ localapi.AvailableCountriesController = (*desktopController)(nil)
