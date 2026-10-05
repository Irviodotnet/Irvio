// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	"github.com/Irviodotnet/Irvio/internal/productprofile"
	"github.com/Irviodotnet/Irvio/internal/productruntime"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/underlay"
)

type stringList []string

func (values *stringList) String() string { return fmt.Sprint([]string(*values)) }

func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type nodeInfo struct {
	PeerID      string   `json:"peer_id"`
	ListenAddrs []string `json:"listen_addrs"`
}

type discoveryResult struct {
	Country   string   `json:"country"`
	Providers []string `json:"providers"`
}

func main() {
	// Snapshot before Local API adoption consumes the activation environment.
	socketActivated := os.Getenv("LISTEN_FDS") == "1" && os.Getenv("LISTEN_PID") == fmt.Sprint(os.Getpid())
	logStore, err := localapi.NewLogStore(localapi.DefaultLogCapacity)
	if err != nil {
		fmt.Fprintln(os.Stderr, "irviod: initialize product log buffer:", err)
		os.Exit(1)
	}
	logger := slog.New(localapi.NewMultiHandler(
		slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}),
		logStore,
	))
	if err := run(logger, logStore); errors.Is(err, errProfileResetComplete) {
		restartErr := completeProfileReset(logger, socketActivated, func() error {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			return syscall.Exec(executable, os.Args, os.Environ())
		})
		if restartErr != nil {
			logger.Error("PROFILE_RESTART_FAILED", "reason", restartErr)
			os.Exit(1)
		}
	} else if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("IRVIOD_EXIT", "reason", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, logStore *localapi.LogStore) (runErr error) {
	var bootstraps, underlayValues, bypassValues, tunnelDNSValues, exitTUNValues, managementValues, verifierValues, rendezvousValues stringList
	roleFlag := flag.String("role", "", "product role: desktop or server (default: desktop)")
	bootRecover := flag.Bool("boot-recover", false, "internal: reconcile previous-boot IRVIO state and exit")
	dataDir := flag.String("data-dir", "", "node data directory")
	listenPort := flag.Int("listen-port", 0, "TCP listen port (0 selects an ephemeral port)")
	flag.Var(&bootstraps, "bootstrap", "bootstrap literal-IP /p2p multiaddr; may be repeated")
	exit := flag.Bool("exit", false, "advertise and run the EXIT capability")
	country := flag.String("country", "", "two-letter Exit country")
	findExits := flag.String("find-exits", "", "discover exits for a two-letter country")
	findOnce := flag.Bool("find-once", false, "exit after --find-exits completes")
	findTimeout := flag.Duration("find-timeout", 20*time.Second, "discovery deadline")
	dhtMode := flag.String("dht-mode", "server", "Kademlia mode: server or auto")
	printNodeInfo := flag.Bool("print-node-info", false, "write one JSON node info object to stdout")
	printRuntimeEvents := flag.Bool("print-runtime-events", false, "write redacted runtime lifecycle events to stderr")
	protectedMode := flag.Bool("protected-mode", false, "keep ordinary local traffic fail-closed while Irvio is active")
	rotateAWGKey := flag.Bool("rotate-awg-key", false, "rotate the persistent AWG transport key before publishing")
	publicNode := flag.Bool("public-node", false, "request independently verified public control-node capability")
	publicPort4 := flag.Int("public-control-port4", 0, "advertised external IPv4 TCP control port (0 uses listener port)")
	publicPort6 := flag.Int("public-control-port6", 0, "advertised external IPv6 TCP control port (0 uses listener port)")
	publicProofTTL := flag.Duration("public-proof-ttl", 5*time.Minute, "public reachability proof lifetime")
	publicReverifyLead := flag.Duration("public-reverify-lead", time.Minute, "public proof renewal lead time")
	publicRetry := flag.Duration("public-retry", time.Minute, "failed public verification retry delay")
	allowTestPublicIP := flag.Bool("allow-test-public-ip", false, "LAB ONLY: permit private/documentation addresses in reachability proofs")
	flag.Var(&verifierValues, "reachability-verifier", "literal-IP verifier /p2p multiaddr; repeat 2-3 times")
	m12Enabled := flag.Bool("rendezvous-v1", false, "use Rendezvous v1 discovery and signed brokered session establishment")
	m12Serve := flag.Bool("rendezvous-service", false, "serve Rendezvous v1 after M11 public reachability verification")
	flag.Var(&rendezvousValues, "rendezvous-peer", "literal-IP Rendezvous /p2p multiaddr; may be repeated")
	geoIPDatabase := flag.String("geoip-db", "", "local GeoIP country MMDB used by the Rendezvous service")
	rendezvousDescriptorTTL := flag.Duration("rendezvous-descriptor-ttl", 10*time.Minute, "signed Rendezvous descriptor lifetime")
	rendezvousReceiptTTL := flag.Duration("rendezvous-receipt-ttl", 10*time.Minute, "Rendezvous registration receipt lifetime")
	sessionControlPort := flag.Int("session-control-port", 51822, "inner TCP/Noise session-control port for M12 Exit leases")
	localAPISocket := flag.String("local-api-socket", "", "Unix socket for IRVIO Local API v1 (empty disables it; production default is /run/irvio/control.sock)")
	localAPIGroup := flag.String("local-api-group", "irvio", "authorized service group for a root-owned Local API socket")

	production := flag.Bool("runtime", false, "enable the production M0-M10 transport runtime")
	connectCountry := flag.String("connect", "", "enable production runtime and connect to this explicit country")
	udpPort := flag.Int("udp-port", 0, "preferred Irvio UDP port (0 selects an ephemeral port)")
	stun4 := flag.String("stun4", "", "literal IPv4 STUN endpoint")
	stun6 := flag.String("stun6", "", "literal IPv6 STUN endpoint")
	tunName := flag.String("tun-name", "hydra0", "logical Irvio TUN name")
	exitWAN := flag.String("exit-wan", "", "Exit physical WAN interface")
	exitIPv4Pool := flag.String("exit-ipv4-pool", "", "Exit-local downstream IPv4 pool")
	exitIPv6Pool := flag.String("exit-ipv6-pool", "", "Exit-local downstream IPv6 pool")
	leaseTTL := flag.Duration("lease-ttl", tunnellease.DefaultTTL, "authenticated TunnelLease TTL")
	leaseSweep := flag.Duration("lease-sweep", time.Second, "TunnelLease expiry reconciliation interval")
	leaseRenewLead := flag.Duration("lease-renew-lead", 0, "renewal lead time (0 scales safely from lease TTL)")
	leaseRenewRetry := flag.Duration("lease-renew-retry", 0, "renewal retry delay (0 scales safely from lease TTL)")
	flag.Var(&underlayValues, "underlay-target", "literal marked challenge IP:port or authenticated /p2p multiaddr; may be repeated")
	flag.Var(&bypassValues, "bypass-prefix", "physical control/LAN prefix bypass; may be repeated")
	flag.Var(&tunnelDNSValues, "tunnel-dns", "literal DNS server routed through hydra0 while connected; may be repeated")
	flag.Var(&exitTUNValues, "exit-tun-address", "Exit hydra0 address prefix; may be repeated")
	flag.Var(&managementValues, "exit-management-prefix", "Exit-local destination to isolate; may be repeated")
	flag.Parse()
	if *bootRecover {
		if flag.NFlag() != 1 || flag.NArg() != 0 {
			return fmt.Errorf("--boot-recover cannot be combined with other options")
		}
		return recoverPreviousBoot(context.Background())
	}
	if *roleFlag != "" || flag.NFlag() == 0 {
		fileConfig, err := productruntime.LoadConfig(productruntime.DefaultConfigPath)
		if err != nil {
			return err
		}
		role, err := productruntime.ResolveRole(fileConfig, *roleFlag)
		if err != nil {
			return err
		}
		if flag.NArg() != 0 {
			return fmt.Errorf("unexpected product CLI positional arguments")
		}
		allowed := map[string]bool{"role": true, "bootstrap": true, "data-dir": true,
			"local-api-socket": true, "local-api-group": true, "allow-test-public-ip": true,
			"geoip-db": true, "public-proof-ttl": true, "public-reverify-lead": true,
			"public-retry": true}
		var unsupported string
		flag.Visit(func(value *flag.Flag) {
			if !allowed[value.Name] && unsupported == "" {
				unsupported = value.Name
			}
		})
		if unsupported != "" {
			return fmt.Errorf("--%s is not yet resolved by the product preset; use the legacy engineering CLI without --role", unsupported)
		}
		if *publicProofTTL <= 0 || *publicReverifyLead <= 0 || *publicReverifyLead >= *publicProofTTL || *publicRetry <= 0 {
			return fmt.Errorf("invalid M11 reachability timing override")
		}
		return runProduct(productOptions{role: role, dataDir: *dataDir, bootstrap: bootstraps,
			apiSocket: *localAPISocket, apiGroup: *localAPIGroup, allowTest: *allowTestPublicIP,
			geoipDB: *geoIPDatabase, publicProofTTL: *publicProofTTL,
			publicReverify: *publicReverifyLead, publicRetry: *publicRetry,
			logger: logger, logStore: logStore})
	}

	if *dataDir == "" {
		return fmt.Errorf("--data-dir is required")
	}
	profile, err := productprofile.Open(*dataDir)
	if err != nil {
		return fmt.Errorf("open product profile: %w", err)
	}
	resetRequested := false
	var shutdownErr error
	defer func() {
		if !resetRequested {
			return
		}
		if shutdownErr != nil {
			runErr = fmt.Errorf("profile deletion blocked by shutdown failure: %w", shutdownErr)
			return
		}
		if err := profile.DeleteContents(); err != nil {
			runErr = fmt.Errorf("delete product profile: %w", err)
			return
		}
		runErr = errProfileResetComplete
	}()
	if *findOnce && *findExits == "" {
		return fmt.Errorf("--find-once requires --find-exits")
	}
	if *publicPort4 < 0 || *publicPort4 > 65535 || *publicPort6 < 0 || *publicPort6 > 65535 ||
		*publicProofTTL <= 0 || *publicReverifyLead <= 0 || *publicReverifyLead >= *publicProofTTL || *publicRetry <= 0 {
		return fmt.Errorf("invalid public reachability configuration")
	}
	if *sessionControlPort < 1 || *sessionControlPort > 65535 || *m12Serve && !*m12Enabled ||
		*rendezvousDescriptorTTL < time.Second || *rendezvousDescriptorTTL > 10*time.Minute ||
		*rendezvousReceiptTTL < time.Second || *rendezvousReceiptTTL > *rendezvousDescriptorTTL {
		return fmt.Errorf("invalid M12 Rendezvous configuration")
	}
	m12Policy := rendezvous.DefaultConfig()
	m12Policy.DescriptorTTL = *rendezvousDescriptorTTL
	m12Policy.ReceiptTTL = *rendezvousReceiptTTL
	m12Policy.ReceiptRenewLead = *rendezvousReceiptTTL / 2
	var productionConfig *irvioruntime.ProductionConfig
	var targets irvioruntime.StaticTargets
	if *production || *connectCountry != "" {
		config, parsedTargets, err := parseProductionConfig(
			*udpPort, *stun4, *stun6, *tunName, *connectCountry,
			*exitWAN, *exitIPv4Pool, *exitIPv6Pool, *leaseTTL, *leaseSweep,
			*leaseRenewLead, *leaseRenewRetry,
			underlayValues, bypassValues, tunnelDNSValues, exitTUNValues, managementValues,
		)
		if err != nil {
			return err
		}
		productionConfig, targets = config, parsedTargets
	}
	if *localAPISocket != "" && productionConfig == nil {
		return fmt.Errorf("--local-api-socket requires --runtime or --connect")
	}
	root, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	rotationSignals := make(chan os.Signal, 1)
	signal.Notify(rotationSignals, syscall.SIGHUP)
	defer signal.Stop(rotationSignals)
	resolvedBootstraps, err := productruntime.ExpandBootstrap(bootstraps)
	if err != nil {
		return err
	}
	bootstrapManager, err := productbootstrap.New(productbootstrap.Config{
		DataDir: *dataDir, CLI: resolvedBootstraps, Logger: logger, AllowTestAddresses: *allowTestPublicIP,
	})
	if err != nil {
		return err
	}
	startupBootstraps, err := bootstrapManager.StartupAddresses()
	if err != nil {
		return err
	}
	daemon, err := irvioruntime.NewDaemon(irvioruntime.DaemonConfig{
		DataDir: *dataDir, ListenPort: *listenPort, Bootstrap: startupBootstraps,
		Exit: *exit, Country: *country, Logger: logger, DHTMode: *dhtMode,
		ProtectedMode: *protectedMode, RotateAWGKey: *rotateAWGKey,
		Production: productionConfig, UnderlayTargets: targets,
		PublicRole: irvioruntime.PublicRoleConfig{
			Requested: *publicNode, VerifierAddresses: verifierValues,
			ExternalPortIPv4: uint16(*publicPort4), ExternalPortIPv6: uint16(*publicPort6),
			ProofTTL: *publicProofTTL, ReverifyLead: *publicReverifyLead, RetryDelay: *publicRetry,
			AllowTestAddresses: *allowTestPublicIP, OperationTimeout: 10 * time.Second, Logger: logger,
		},
		M12: irvioruntime.M12Config{
			Enabled: *m12Enabled, Serve: *m12Serve, GeoIPDatabase: *geoIPDatabase,
			RendezvousPeers: rendezvousValues, SessionControlPort: uint16(*sessionControlPort),
			AllowTestAddresses: *allowTestPublicIP, Policy: m12Policy,
		},
	})
	if err != nil {
		return err
	}
	if err := daemon.Start(root); err != nil {
		return err
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := daemon.Close(closeContext)
		shutdownErr = errors.Join(shutdownErr, err)
		runErr = errors.Join(runErr, err)
	}()
	if err := bootstrapManager.Attach(root, daemon.Node()); err != nil {
		return err
	}
	defer bootstrapManager.Close()
	profileReset := newProfileResetController(daemon)
	var localAPIServer *localapi.Server
	if *localAPISocket != "" {
		server, err := newLocalAPIServer(
			*localAPISocket, *localAPIGroup, daemon, daemon, logger, logStore, bootstrapManager, profileReset, 0,
		)
		if err != nil {
			logger.Error("LOCAL_API_START_FAILED", "socket", *localAPISocket, "reason", err)
		} else if err := server.Start(root); err != nil {
			logger.Error("LOCAL_API_START_FAILED", "socket", *localAPISocket, "reason", err)
		} else {
			localAPIServer = server
			logger.Info("LOCAL_API_STARTED", "socket", *localAPISocket, "api_major", localapi.APIMajor, "api_minor", localapi.APIMinor)
			defer func() {
				err := server.Close()
				shutdownErr = errors.Join(shutdownErr, err)
				runErr = errors.Join(runErr, err)
			}()
		}
	}
	node := daemon.Node()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if *printNodeInfo {
		if err := encoder.Encode(nodeInfo{PeerID: node.PeerID().String(), ListenAddrs: node.ListenAddresses()}); err != nil {
			return fmt.Errorf("print node info: %w", err)
		}
	}
	if events := daemon.Events(); events != nil && (*printRuntimeEvents || localAPIServer != nil) {
		go func() {
			for event := range events {
				if localAPIServer != nil {
					localAPIServer.ObserveRuntimeStatus(event.Status)
				}
				if *printRuntimeEvents {
					logRuntimeEvent(logger, event)
				}
			}
		}()
	}
	if *findExits != "" {
		findContext, cancel := context.WithTimeout(root, *findTimeout)
		batch, err := daemon.Candidates(findContext, *findExits, 10)
		cancel()
		if err != nil {
			return err
		}
		if len(batch.Candidates) == 0 {
			return fmt.Errorf("no verified exits found for %s", *findExits)
		}
		providerStrings := make([]string, len(batch.Candidates))
		for index, candidate := range batch.Candidates {
			providerStrings[index] = candidate.PeerID.String()
		}
		if err := encoder.Encode(discoveryResult{Country: *findExits, Providers: providerStrings}); err != nil {
			return fmt.Errorf("print discovery result: %w", err)
		}
		if *findOnce {
			return nil
		}
	}
	for {
		select {
		case <-root.Done():
			return nil
		case <-profileReset.Committed():
			resetRequested = true
			return errProfileResetRequested
		case <-rotationSignals:
			rotationContext, cancel := context.WithTimeout(root, 2*time.Minute)
			err := daemon.RotateAWGKey(rotationContext)
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Warn("AWG_DIRECT_ROTATION_FAILED", "reason", err)
			}
		}
	}
}

func logRuntimeEvent(logger *slog.Logger, event irvioruntime.Event) {
	logger.Info("IRVIOD_RUNTIME_EVENT",
		"state", event.Status.State, "reason", event.Status.LastReason,
		"runtime_generation", event.Status.RuntimeGeneration,
		"network_generation", event.Status.NetworkGeneration,
		"transport_generation", event.Status.TransportGeneration,
		"session_generation", event.Status.SessionGeneration,
		"active_peer", event.Status.ActivePeer,
		"reserve_peer", event.Status.ReservePeer,
		"cold_peer", event.Status.ColdPeer,
		"tunnel_lease_id", event.Status.TunnelLeaseID,
		"tunnel_lease_expiry", event.Status.TunnelLeaseExpiry,
		"awg_key_epoch", event.Status.AWGKeyEpoch,
		"selected_endpoint", event.Status.SelectedEndpoint,
		"mux_port", event.Status.MuxPort,
		"protected_mode", event.Status.ProtectedMode,
		"protection_adopted", event.Status.ProtectionAdopted,
		"underlay_ipv4", event.Status.Underlay.IPv4.State,
		"underlay_ipv6", event.Status.Underlay.IPv6.State,
		"error", event.Err,
	)
}

func newLocalAPIServer(
	path, groupName string,
	controller localapi.RuntimeController,
	countries localapi.AvailableCountriesController,
	logger *slog.Logger,
	logStore *localapi.LogStore,
	bootstrapManager localapi.BootstrapController,
	profile localapi.ProfileController,
	requestTimeout time.Duration,
	shutdown ...localapi.ShutdownController,
) (*localapi.Server, error) {
	config := localapi.DefaultServerConfig(path)
	if requestTimeout > 0 {
		config.RequestTimeout = requestTimeout
		config.DisconnectTimeout = time.Minute
	}
	config.Logger = logger
	config.Logs = logStore
	config.Bootstrap = bootstrapManager
	config.Profile = profile
	config.Countries = countries
	if len(shutdown) != 0 {
		config.Shutdown = shutdown[0]
	}
	if os.Geteuid() != 0 {
		return localapi.NewServer(config, controller)
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return nil, fmt.Errorf("resolve local API group %q: %w", groupName, err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil || gid < 0 {
		return nil, fmt.Errorf("invalid local API group ID %q", group.Gid)
	}
	config.OwnerUID = 0
	config.OwnerGID = gid
	selectedUID := os.Getenv("IRVIO_LOCAL_API_UID")
	if selectedUID != "" && (os.Getenv("LISTEN_FDS") != "1" || os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid())) {
		return nil, errors.New("IRVIO_LOCAL_API_UID requires systemd socket activation")
	}
	policy, err := localAPICredentialPolicy(selectedUID, uint32(gid))
	if err != nil {
		return nil, err
	}
	config.Authorizer = policy
	return localapi.NewServer(config, controller)
}

// Packaged desktops authorize the selected UID, independent of login-session groups.
func localAPICredentialPolicy(selectedUID string, gid uint32) (localapi.CredentialPolicy, error) {
	policy := localapi.CredentialPolicy{AllowedUIDs: map[uint32]struct{}{0: {}}}
	if selectedUID == "" {
		policy.AllowedGIDs = map[uint32]struct{}{gid: {}}
		return policy, nil
	}
	uid, err := strconv.ParseUint(selectedUID, 10, 32)
	if err != nil || uid < 1000 || uid == 1<<32-1 || strconv.FormatUint(uid, 10) != selectedUID {
		return localapi.CredentialPolicy{}, fmt.Errorf("invalid desktop Local API UID %q", selectedUID)
	}
	policy.AllowedUIDs[uint32(uid)] = struct{}{}
	return policy, nil
}

func parseProductionConfig(
	udpPort int, stun4, stun6, tunName, connectCountry, exitWAN, pool4, pool6 string,
	leaseTTL, leaseSweep, leaseRenewLead, leaseRenewRetry time.Duration,
	targetValues, bypassValues, tunnelDNSValues, exitTUNValues, managementValues []string,
) (*irvioruntime.ProductionConfig, irvioruntime.StaticTargets, error) {
	if udpPort < 0 || udpPort > 65535 || leaseTTL <= 0 || leaseSweep <= 0 ||
		leaseRenewLead < 0 || leaseRenewRetry < 0 || leaseRenewLead >= leaseTTL || leaseRenewRetry >= leaseTTL {
		return nil, nil, errors.New("invalid production runtime limits")
	}
	stunIPv4, err := parseOptionalAddrPort(stun4, true)
	if err != nil {
		return nil, nil, fmt.Errorf("--stun4: %w", err)
	}
	stunIPv6, err := parseOptionalAddrPort(stun6, false)
	if err != nil {
		return nil, nil, fmt.Errorf("--stun6: %w", err)
	}
	ipv4Pool, err := parseOptionalPrefix(pool4, true)
	if err != nil {
		return nil, nil, fmt.Errorf("--exit-ipv4-pool: %w", err)
	}
	ipv6Pool, err := parseOptionalPrefix(pool6, false)
	if err != nil {
		return nil, nil, fmt.Errorf("--exit-ipv6-pool: %w", err)
	}
	exitAddresses, err := parsePrefixes(exitTUNValues)
	if err != nil {
		return nil, nil, fmt.Errorf("--exit-tun-address: %w", err)
	}
	bypass, err := parsePrefixes(bypassValues)
	if err != nil {
		return nil, nil, fmt.Errorf("--bypass-prefix: %w", err)
	}
	tunnelDNS, err := parseTunnelDNSServers(tunnelDNSValues)
	if err != nil {
		return nil, nil, fmt.Errorf("--tunnel-dns: %w", err)
	}
	management, err := parsePrefixes(managementValues)
	if err != nil {
		return nil, nil, fmt.Errorf("--exit-management-prefix: %w", err)
	}
	targets := make(irvioruntime.StaticTargets, 0, len(targetValues))
	for _, value := range targetValues {
		target, err := parseUnderlayTarget(value)
		if err != nil {
			return nil, nil, fmt.Errorf("--underlay-target %q: %w", value, err)
		}
		targets = append(targets, target)
	}
	return &irvioruntime.ProductionConfig{
		UDPPort: udpPort, STUNIPv4: stunIPv4, STUNIPv6: stunIPv6,
		TUNName: tunName, TunnelDNSServers: tunnelDNS, Bypass: bypass,
		Obfuscation:    irvioruntime.DefaultProductionObfuscation(),
		ConnectCountry: exitselector.CountryIntent(strings.ToUpper(connectCountry)),
		ExitWANName:    exitWAN, ExitTUNAddresses: exitAddresses,
		ExitIPv4Pool: ipv4Pool, ExitIPv6Pool: ipv6Pool, ExitManagement: management,
		LeaseTTL: leaseTTL, LeaseSweep: leaseSweep,
		LeaseRenewLead: leaseRenewLead, LeaseRenewRetry: leaseRenewRetry,
	}, targets, nil
}

func parseTunnelDNSServers(values []string) ([]netip.Addr, error) {
	if len(values) > 8 {
		return nil, errors.New("at most 8 literal DNS servers are allowed")
	}
	result := make([]netip.Addr, 0, len(values))
	seen := make(map[netip.Addr]struct{}, len(values))
	for _, value := range values {
		server, err := netip.ParseAddr(value)
		if err != nil || server.IsUnspecified() || server.IsLoopback() || server.IsLinkLocalUnicast() ||
			server.IsMulticast() || server.Is4In6() || server.Zone() != "" {
			return nil, fmt.Errorf("%q is not a usable literal IP address", value)
		}
		if _, ok := seen[server]; ok {
			continue
		}
		seen[server] = struct{}{}
		result = append(result, server)
	}
	return result, nil
}

func parseUnderlayTarget(value string) (underlay.Target, error) {
	if endpoint, err := netip.ParseAddrPort(value); err == nil && endpoint.IsValid() && endpoint.Port() != 0 {
		return underlay.Target{Endpoint: endpoint}, nil
	}
	address, err := ma.NewMultiaddr(value)
	if err != nil {
		return underlay.Target{}, errors.New("expected a literal IP:port or literal-IP /p2p multiaddr")
	}
	info, err := peer.AddrInfoFromP2pAddr(address)
	if err != nil || info.ID == "" || len(info.Addrs) != 1 {
		return underlay.Target{}, errors.New("expected a single literal-IP /p2p multiaddr")
	}
	endpoint, ok := underlayEndpoint(info.Addrs[0])
	if !ok {
		return underlay.Target{}, errors.New("expected a literal-IP TCP /p2p multiaddr")
	}
	return underlay.Target{PeerID: info.ID, Endpoint: endpoint}, nil
}

func underlayEndpoint(address ma.Multiaddr) (netip.AddrPort, bool) {
	value, err4 := address.ValueForProtocol(ma.P_IP4)
	if err4 != nil {
		value, _ = address.ValueForProtocol(ma.P_IP6)
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.Is4In6() {
		return netip.AddrPort{}, false
	}
	portValue, err := address.ValueForProtocol(ma.P_TCP)
	if err != nil {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(portValue, 10, 16)
	if err != nil || port == 0 {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ip, uint16(port)), true
}

func parseOptionalAddrPort(value string, ipv4 bool) (netip.AddrPort, error) {
	if value == "" {
		return netip.AddrPort{}, nil
	}
	endpoint, err := netip.ParseAddrPort(value)
	if err != nil || endpoint.Addr().Is4() != ipv4 || endpoint.Addr().Is4In6() || endpoint.Port() == 0 {
		return netip.AddrPort{}, errors.New("expected a literal family-correct IP:port")
	}
	return endpoint, nil
}

func parseOptionalPrefix(value string, ipv4 bool) (netip.Prefix, error) {
	if value == "" {
		return netip.Prefix{}, nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || prefix.Addr().Is4() != ipv4 || prefix.Addr().Is4In6() {
		return netip.Prefix{}, errors.New("expected a family-correct prefix")
	}
	return prefix.Masked(), nil
}

func parsePrefixes(values []string) ([]netip.Prefix, error) {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("invalid prefix %q", value)
		}
		result = append(result, prefix)
	}
	return result, nil
}
