// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package localapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"hydra.local/hydra/internal/exitselector"
	"hydra.local/hydra/internal/productbootstrap"
	"hydra.local/hydra/internal/rendezvous"
	"hydra.local/hydra/internal/runtime"
	"hydra.local/hydra/internal/trafficstats"
	localv1 "hydra.local/hydra/proto/hydra/local/v1"
)

const (
	APIMajor          uint32 = 1
	APIMinor          uint32 = 5
	CoreVersion              = "v1"
	DefaultSocketPath        = "/run/irvio/control.sock"
)

var (
	ErrInvalidConfig = errors.New("invalid local API configuration")
	ErrServerClosed  = errors.New("local API server is closed")
	ErrSocketInUse   = errors.New("local API socket already has a live listener")
)

type RuntimeController interface {
	Status() runtime.Status
	Connect(context.Context, exitselector.CountryIntent) error
	Disconnect(context.Context) error
}

type BootstrapController interface {
	State() *localv1.BootstrapState
	Import(string) (*localv1.BootstrapState, error)
	Generate(context.Context, int) (*localv1.GeneratedBootstrapBundle, error)
}

type ProfileController interface {
	PrepareDelete(context.Context) error
	CommitDelete()
	CancelDelete()
}

type AvailableCountriesController interface {
	AvailableCountries(context.Context, bool) (rendezvous.AvailableCountries, error)
}

type ShutdownController interface {
	PrepareShutdown(context.Context) error
	CommitShutdown()
}

type ServerConfig struct {
	SocketPath        string
	DirectoryMode     os.FileMode
	SocketMode        os.FileMode
	OwnerUID          int
	OwnerGID          int
	Authorizer        Authorizer
	MaxClients        int
	EventQueue        int
	PollInterval      time.Duration
	IdleTimeout       time.Duration
	FrameTimeout      time.Duration
	WriteTimeout      time.Duration
	RequestTimeout    time.Duration
	DisconnectTimeout time.Duration
	ProductVersion    string
	Logger            *slog.Logger
	Logs              *LogStore
	Bootstrap         BootstrapController
	Profile           ProfileController
	Countries         AvailableCountriesController
	Shutdown          ShutdownController
	Now               func() time.Time
	BeforeWriteFrame  func(*localv1.Envelope)
}

func DefaultServerConfig(socketPath string) ServerConfig {
	uid, gid := os.Geteuid(), os.Getegid()
	return ServerConfig{
		SocketPath: socketPath, DirectoryMode: 0o750, SocketMode: 0o660,
		OwnerUID: uid, OwnerGID: gid,
		Authorizer: CredentialPolicy{
			AllowedUIDs: map[uint32]struct{}{uint32(uid): {}},
			AllowedGIDs: map[uint32]struct{}{uint32(gid): {}},
		},
		MaxClients: 8, EventQueue: 64, PollInterval: 100 * time.Millisecond,
		IdleTimeout: 10 * time.Minute, FrameTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, RequestTimeout: 5 * time.Second,
		DisconnectTimeout: 20 * time.Second,
		ProductVersion:    "development", Logger: slog.Default(), Now: time.Now,
	}
}

type Server struct {
	config     ServerConfig
	controller RuntimeController

	mu          sync.Mutex
	listener    *net.UnixListener
	socketInfo  os.FileInfo
	sessions    map[*serverSession]struct{}
	subscribers map[*serverSession]struct{}
	current     *localv1.Status
	sequence    uint64
	connectedAt time.Time
	closed      bool
	cancel      context.CancelFunc
	wait        sync.WaitGroup
}

func NewServer(config ServerConfig, controller RuntimeController) (*Server, error) {
	if controller == nil || config.SocketPath == "" || !filepath.IsAbs(config.SocketPath) ||
		config.Authorizer == nil || config.MaxClients < 1 || config.MaxClients > 64 ||
		config.EventQueue < 1 || config.EventQueue > 1024 || config.PollInterval <= 0 ||
		config.IdleTimeout <= 0 || config.FrameTimeout <= 0 || config.WriteTimeout <= 0 ||
		config.RequestTimeout <= 0 || config.DisconnectTimeout < config.RequestTimeout ||
		config.DisconnectTimeout > time.Minute || config.DirectoryMode.Perm()&0o007 != 0 ||
		config.SocketMode.Perm()&0o007 != 0 || config.SocketMode.Perm() == 0 || config.Now == nil {
		return nil, ErrInvalidConfig
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Server{
		config: config, controller: controller,
		sessions: make(map[*serverSession]struct{}), subscribers: make(map[*serverSession]struct{}),
	}, nil
}

func (server *Server) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	server.mu.Lock()
	if server.listener != nil {
		server.mu.Unlock()
		return ErrSocketInUse
	}
	if server.closed {
		server.mu.Unlock()
		return ErrServerClosed
	}
	server.mu.Unlock()

	listener, info, err := server.listen()
	if err != nil {
		return err
	}
	runContext, cancel := context.WithCancel(ctx)
	server.mu.Lock()
	server.listener = listener
	server.socketInfo = info
	server.cancel = cancel
	server.current = server.projectStatusLocked(server.controller.Status())
	server.sequence = 1
	server.mu.Unlock()

	server.wait.Add(2)
	go server.acceptLoop(runContext)
	go server.statusLoop(runContext)
	return nil
}

func (server *Server) Close() error {
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return nil
	}
	server.closed = true
	listener, cancel := server.listener, server.cancel
	sessions := make([]*serverSession, 0, len(server.sessions))
	for session := range server.sessions {
		sessions = append(sessions, session)
	}
	server.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var closeErr error
	if listener != nil {
		closeErr = listener.Close()
	}
	for _, session := range sessions {
		session.close()
	}
	server.wait.Wait()
	server.removeOwnedSocket()
	return closeErr
}

func (server *Server) SessionCount() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return len(server.sessions)
}

func (server *Server) listen() (*net.UnixListener, os.FileInfo, error) {
	if listener, err := inheritedSystemdListener(server.config.SocketPath); listener != nil || err != nil {
		return listener, nil, err
	}
	parent := filepath.Dir(server.config.SocketPath)
	parentInfo, err := os.Lstat(parent)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(parent, server.config.DirectoryMode.Perm()); err != nil {
			return nil, nil, fmt.Errorf("create local API directory: %w", err)
		}
		if err := os.Chown(parent, server.config.OwnerUID, server.config.OwnerGID); err != nil {
			return nil, nil, fmt.Errorf("own local API directory: %w", err)
		}
		parentInfo, err = os.Lstat(parent)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect local API directory: %w", err)
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("local API parent is not a real directory")
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !ok || int(parentStat.Uid) != server.config.OwnerUID || int(parentStat.Gid) != server.config.OwnerGID ||
		parentInfo.Mode().Perm()&0o007 != 0 {
		if !ok {
			return nil, nil, errors.New("local API parent ownership is unavailable")
		}
		return nil, nil, fmt.Errorf("local API parent has unsafe ownership or permissions: uid=%d gid=%d mode=%04o", parentStat.Uid, parentStat.Gid, parentInfo.Mode().Perm())
	}
	if err := server.removeStaleSocket(); err != nil {
		return nil, nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: server.config.SocketPath, Net: "unix"})
	if err != nil {
		return nil, nil, fmt.Errorf("listen on local API socket: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	fail := func(operation error) (*net.UnixListener, os.FileInfo, error) {
		_ = listener.Close()
		_ = os.Remove(server.config.SocketPath)
		return nil, nil, operation
	}
	if err := os.Chmod(server.config.SocketPath, server.config.SocketMode.Perm()); err != nil {
		return fail(fmt.Errorf("set local API socket mode: %w", err))
	}
	if err := os.Chown(server.config.SocketPath, server.config.OwnerUID, server.config.OwnerGID); err != nil {
		return fail(fmt.Errorf("own local API socket: %w", err))
	}
	info, err := os.Lstat(server.config.SocketPath)
	if err != nil {
		return fail(fmt.Errorf("inspect local API socket: %w", err))
	}
	return listener, info, nil
}

func inheritedSystemdListener(path string) (*net.UnixListener, error) {
	fds, pid := os.Getenv("LISTEN_FDS"), os.Getenv("LISTEN_PID")
	if fds == "" && pid == "" {
		return nil, nil
	}
	if fds != "1" || pid != fmt.Sprint(os.Getpid()) {
		return nil, errors.New("invalid systemd Local API socket activation environment")
	}
	file := os.NewFile(3, "systemd-hydra-control")
	if file == nil {
		return nil, errors.New("systemd Local API descriptor is missing")
	}
	defer file.Close()
	listener, err := net.FileListener(file)
	if err != nil {
		return nil, fmt.Errorf("adopt systemd Local API socket: %w", err)
	}
	unix, ok := listener.(*net.UnixListener)
	address, addressOK := listener.Addr().(*net.UnixAddr)
	if !ok || !addressOK || address.Name != path {
		_ = listener.Close()
		return nil, errors.New("systemd Local API descriptor has unexpected type or path")
	}
	unix.SetUnlinkOnClose(false)
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDNAMES")
	return unix, nil
}

func (server *Server) removeStaleSocket() error {
	info, err := os.Lstat(server.config.SocketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing local API path: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("refusing to replace non-socket local API path")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != server.config.OwnerUID || int(stat.Gid) != server.config.OwnerGID {
		return errors.New("refusing to replace foreign local API socket")
	}
	connection, dialErr := net.DialTimeout("unix", server.config.SocketPath, 100*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return ErrSocketInUse
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return fmt.Errorf("cannot prove local API socket is stale: %w", dialErr)
	}
	if err := os.Remove(server.config.SocketPath); err != nil {
		return fmt.Errorf("remove stale local API socket: %w", err)
	}
	return nil
}

func (server *Server) removeOwnedSocket() {
	server.mu.Lock()
	expected := server.socketInfo
	path := server.config.SocketPath
	server.mu.Unlock()
	if expected == nil {
		return
	}
	current, err := os.Lstat(path)
	if err == nil && current.Mode()&os.ModeSocket != 0 && os.SameFile(expected, current) {
		_ = os.Remove(path)
	}
}

func (server *Server) acceptLoop(ctx context.Context) {
	defer server.wait.Done()
	for {
		connection, err := server.listener.AcceptUnix()
		if err != nil {
			if ctx.Err() == nil {
				server.config.Logger.Warn("LOCAL_API_ACCEPT_FAILED", "reason", err)
			}
			return
		}
		if _, err := server.config.Authorizer.Authorize(connection); err != nil {
			server.writeUnauthorized(connection)
			_ = connection.Close()
			continue
		}
		server.mu.Lock()
		if server.closed || len(server.sessions) >= server.config.MaxClients {
			server.mu.Unlock()
			_ = connection.Close()
			continue
		}
		session := newServerSession(server, connection)
		server.sessions[session] = struct{}{}
		server.wait.Add(1)
		server.mu.Unlock()
		go session.run(ctx)
	}
}

func (server *Server) writeUnauthorized(connection *net.UnixConn) {
	_ = connection.SetWriteDeadline(time.Now().Add(server.config.WriteTimeout))
	_ = WriteFrame(connection, responseEnvelope(0, localv1.ResultCode_RESULT_CODE_UNAUTHORIZED, "local client is not authorized"))
}

func (server *Server) statusLoop(ctx context.Context) {
	defer server.wait.Done()
	ticker := time.NewTicker(server.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			server.refreshStatus()
		}
	}
}

func (server *Server) refreshStatus() {
	server.publishRuntimeStatus(server.controller.Status())
}

// ObserveRuntimeStatus publishes exact transitions; periodic snapshots remain authoritative on reconnect.
func (server *Server) ObserveRuntimeStatus(status runtime.Status) {
	server.publishRuntimeStatus(status)
}

func (server *Server) publishRuntimeStatus(runtimeStatus runtime.Status) {
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return
	}
	status := server.projectStatusLocked(runtimeStatus)
	if proto.Equal(status, server.current) {
		server.mu.Unlock()
		return
	}
	server.current = status
	server.sequence++
	sequence := server.sequence
	subscribers := make([]*serverSession, 0, len(server.subscribers))
	for subscriber := range server.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	server.mu.Unlock()
	event := statusEvent(sequence, status)
	for _, subscriber := range subscribers {
		if !subscriber.enqueue(event) {
			subscriber.close()
		}
	}
}

func (server *Server) productSnapshot(status runtime.Status) *localv1.Status {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.projectStatusLocked(status)
}

func (server *Server) projectStatusLocked(status runtime.Status) *localv1.Status {
	productState := mapConnectionState(status)
	if productState == localv1.ConnectionState_CONNECTION_STATE_CONNECTED && server.connectedAt.IsZero() {
		server.connectedAt = server.config.Now().UTC()
	}
	if productState == localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED ||
		productState == localv1.ConnectionState_CONNECTION_STATE_DISCONNECTING {
		server.connectedAt = time.Time{}
	}
	return mapProductStatus(status, server.connectedAt)
}

func (server *Server) beginConnectLifecycle() {
	server.mu.Lock()
	server.connectedAt = time.Time{}
	server.mu.Unlock()
}

func (server *Server) resetConnectLifecycle() {
	server.mu.Lock()
	server.connectedAt = time.Time{}
	server.mu.Unlock()
}

func (server *Server) subscribe(session *serverSession) *localv1.Envelope {
	server.mu.Lock()
	server.subscribers[session] = struct{}{}
	status, sequence := server.current, server.sequence
	server.mu.Unlock()
	return statusEvent(sequence, status)
}

func (server *Server) unregister(session *serverSession) {
	server.mu.Lock()
	delete(server.sessions, session)
	delete(server.subscribers, session)
	server.mu.Unlock()
}

func (server *Server) handle(request *localv1.Request) *localv1.Response {
	return server.handleContext(context.Background(), request)
}

func (server *Server) handleContext(parent context.Context, request *localv1.Request) *localv1.Response {
	if request == nil {
		return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "missing request")
	}
	switch request.Method {
	case localv1.Method_METHOD_GET_TRAFFIC_STATS:
		reader, ok := server.controller.(interface{ TrafficStats() trafficstats.Snapshot })
		if !ok {
			return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "traffic statistics unavailable")
		}
		value := reader.TrafficStats()
		stats := &localv1.TrafficStats{SessionId: value.SessionID, Complete: value.Complete, Active: value.Active}
		if !value.SampledAt.IsZero() {
			stats.SampledAtUnixMs = uint64(value.SampledAt.UnixMilli())
		}
		if value.ClientsValid {
			stats.Clients = &value.Clients
		}
		if value.MyValid {
			stats.MyUploadBytes = &value.MyUpload
		}
		if value.DownloadValid {
			stats.MyDownloadBytes = &value.MyDownload
		}
		if value.SharedValid {
			stats.SharedUploadBytes = &value.SharedUpload
		}
		result := response(localv1.ResultCode_RESULT_CODE_OK, "")
		result.Payload = &localv1.Response_TrafficStats{TrafficStats: stats}
		return result
	case localv1.Method_METHOD_GET_STATUS:
		result := response(localv1.ResultCode_RESULT_CODE_OK, "")
		result.Payload = &localv1.Response_Status{Status: server.productSnapshot(server.controller.Status())}
		return result
	case localv1.Method_METHOD_CONNECT:
		country, ok := validCountry(request.CountryCode)
		if !ok {
			return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "country_code must be an ISO alpha-2 code")
		}
		current := server.controller.Status()
		if strings.EqualFold(string(current.Country), country) && current.State != runtime.StateIdle &&
			current.State != runtime.StateStopped && current.State != runtime.StateClosed && current.State != runtime.StateDisconnecting {
			result := response(localv1.ResultCode_RESULT_CODE_OK, "")
			result.Accepted = true
			return result
		}
		ctx, cancel := context.WithTimeout(context.Background(), server.config.RequestTimeout)
		if current.State == runtime.StateIdle || current.State == runtime.StateStopped || current.State == runtime.StateClosed {
			server.beginConnectLifecycle()
		}
		err := server.controller.Connect(ctx, exitselector.CountryIntent(country))
		cancel()
		server.refreshStatus()
		return commandResponse(err)
	case localv1.Method_METHOD_DISCONNECT:
		current := server.controller.Status()
		if current.State == runtime.StateIdle || current.State == runtime.StateStopped || current.State == runtime.StateClosed ||
			current.State == runtime.StateDisconnecting {
			result := response(localv1.ResultCode_RESULT_CODE_OK, "")
			result.Accepted = true
			return result
		}
		ctx, cancel := context.WithTimeout(context.Background(), server.config.DisconnectTimeout)
		err := server.controller.Disconnect(ctx)
		cancel()
		if errors.Is(err, runtime.ErrNotConnected) {
			err = nil
		}
		if err == nil {
			server.resetConnectLifecycle()
		}
		server.refreshStatus()
		return commandResponse(err)
	case localv1.Method_METHOD_SHUTDOWN:
		if server.config.Shutdown == nil {
			return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "daemon-owned shutdown is unavailable")
		}
		ctx, cancel := context.WithTimeout(context.Background(), server.config.DisconnectTimeout)
		err := server.config.Shutdown.PrepareShutdown(ctx)
		cancel()
		if err == nil {
			server.resetConnectLifecycle()
		}
		server.refreshStatus()
		return commandResponse(err)
	case localv1.Method_METHOD_GET_RECENT_LOGS:
		if server.config.Logs == nil {
			return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "product logs are unavailable")
		}
		limit := MaxLogTailRecords
		if request.GetRecentLogs != nil && request.GetRecentLogs.Limit != 0 {
			limit = int(request.GetRecentLogs.Limit)
		}
		if limit < 1 || limit > MaxLogTailRecords {
			return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "log tail limit is invalid")
		}
		result := response(localv1.ResultCode_RESULT_CODE_OK, "")
		result.Payload = &localv1.Response_LogTail{LogTail: server.config.Logs.Tail(limit)}
		return result
	case localv1.Method_METHOD_GET_BOOTSTRAP_STATE:
		if server.config.Bootstrap == nil {
			return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "bootstrap onboarding is unavailable")
		}
		result := response(localv1.ResultCode_RESULT_CODE_OK, "")
		result.Payload = &localv1.Response_BootstrapState{BootstrapState: server.config.Bootstrap.State()}
		return result
	case localv1.Method_METHOD_IMPORT_BOOTSTRAP_BUNDLE:
		if server.config.Bootstrap == nil {
			return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "bootstrap onboarding is unavailable")
		}
		if request.ImportBootstrapBundle == nil {
			return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "bootstrap bundle is missing")
		}
		state, err := server.config.Bootstrap.Import(request.ImportBootstrapBundle.Code)
		if err != nil {
			return bootstrapErrorResponse(err)
		}
		result := response(localv1.ResultCode_RESULT_CODE_OK, "")
		result.Accepted = true
		result.Payload = &localv1.Response_BootstrapState{BootstrapState: state}
		return result
	case localv1.Method_METHOD_GENERATE_BOOTSTRAP_BUNDLE:
		if server.config.Bootstrap == nil {
			return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "Share Irvio is unavailable")
		}
		preferred := 0
		if request.GenerateBootstrapBundle != nil {
			preferred = int(request.GenerateBootstrapBundle.PreferredNodeCount)
		}
		ctx, cancel := context.WithTimeout(context.Background(), server.config.RequestTimeout)
		bundle, err := server.config.Bootstrap.Generate(ctx, preferred)
		cancel()
		if err != nil {
			return bootstrapErrorResponse(err)
		}
		result := response(localv1.ResultCode_RESULT_CODE_OK, "")
		result.Payload = &localv1.Response_GeneratedBootstrapBundle{GeneratedBootstrapBundle: bundle}
		return result
	case localv1.Method_METHOD_DELETE_PROFILE:
		if server.config.Profile == nil {
			return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "profile deletion is unavailable")
		}
		if request.DeleteProfile == nil || !request.DeleteProfile.Confirm {
			return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "explicit profile deletion confirmation is required")
		}
		ctx, cancel := context.WithTimeout(context.Background(), server.config.RequestTimeout)
		err := server.config.Profile.PrepareDelete(ctx)
		cancel()
		if err != nil {
			return commandResponse(err)
		}
		result := response(localv1.ResultCode_RESULT_CODE_OK, "")
		result.Accepted = true
		return result
	case localv1.Method_METHOD_GET_AVAILABLE_COUNTRIES:
		if server.config.Countries == nil {
			return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "dynamic locations are unavailable")
		}
		refresh := request.GetAvailableCountries != nil && request.GetAvailableCountries.Refresh
		ctx, cancel := context.WithTimeout(parent, server.config.RequestTimeout)
		available, err := server.config.Countries.AvailableCountries(ctx, refresh)
		cancel()
		if err != nil {
			return response(localv1.ResultCode_RESULT_CODE_INTERNAL, "Irvio could not load available locations")
		}
		result := response(localv1.ResultCode_RESULT_CODE_OK, "")
		result.Payload = &localv1.Response_AvailableCountries{AvailableCountries: availableCountriesToProto(available)}
		return result
	case localv1.Method_METHOD_GET_CONTRIBUTION, localv1.Method_METHOD_SET_CONTRIBUTION:
		return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "contribution intent is not exposed by Irvio Core v1")
	case localv1.Method_METHOD_HELLO, localv1.Method_METHOD_SUBSCRIBE_EVENTS, localv1.Method_METHOD_SUBSCRIBE_LOGS:
		return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "method is only valid in its protocol phase")
	default:
		return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "unknown local API method")
	}
}

func bootstrapErrorResponse(err error) *localv1.Response {
	switch {
	case errors.Is(err, productbootstrap.ErrInvalidBundle),
		errors.Is(err, productbootstrap.ErrBundleTooLarge),
		errors.Is(err, productbootstrap.ErrNoBootstrapNodes):
		return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, err.Error())
	case errors.Is(err, productbootstrap.ErrUnsupportedBundle):
		return response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "bootstrap bundle version is unsupported")
	case errors.Is(err, productbootstrap.ErrNotAttached),
		errors.Is(err, productbootstrap.ErrNoReachableBootstrap):
		return response(localv1.ResultCode_RESULT_CODE_NOT_READY, err.Error())
	default:
		return response(localv1.ResultCode_RESULT_CODE_INTERNAL, "Irvio could not update bootstrap information")
	}
}

func validCountry(value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 2 || value[0] < 'A' || value[0] > 'Z' || value[1] < 'A' || value[1] > 'Z' {
		return "", false
	}
	return value, true
}

func commandResponse(err error) *localv1.Response {
	if err == nil {
		result := response(localv1.ResultCode_RESULT_CODE_OK, "")
		result.Accepted = true
		return result
	}
	switch {
	case errors.Is(err, runtime.ErrInvalidConfig):
		return response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "command parameters are invalid")
	case errors.Is(err, runtime.ErrAlreadyConnected), errors.Is(err, runtime.ErrInvalidState):
		return response(localv1.ResultCode_RESULT_CODE_BUSY, "Irvio is busy with another lifecycle operation")
	case errors.Is(err, runtime.ErrNotStarted), errors.Is(err, runtime.ErrClosed):
		return response(localv1.ResultCode_RESULT_CODE_NOT_READY, "Irvio runtime is not ready")
	default:
		return response(localv1.ResultCode_RESULT_CODE_INTERNAL, "Irvio could not accept the command")
	}
}

func response(code localv1.ResultCode, message string) *localv1.Response {
	return &localv1.Response{Code: code, UserMessage: message}
}

func responseEnvelope(requestID uint64, code localv1.ResultCode, message string) *localv1.Envelope {
	return &localv1.Envelope{
		ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: requestID,
		Payload: &localv1.Envelope_Response{Response: response(code, message)},
	}
}

func statusEvent(sequence uint64, status *localv1.Status) *localv1.Envelope {
	return &localv1.Envelope{
		ApiMajor: APIMajor, ApiMinor: APIMinor,
		Payload: &localv1.Envelope_Event{Event: &localv1.Event{
			Sequence: sequence,
			Payload:  &localv1.Event_StatusChanged{StatusChanged: &localv1.StatusChanged{Status: status}},
		}},
	}
}

type serverSession struct {
	server *Server
	conn   *net.UnixConn
	out    chan outboundFrame
	done   chan struct{}
	once   sync.Once

	logMu           sync.Mutex
	logCancel       func()
	logWait         sync.WaitGroup
	negotiatedMinor uint32
}

type outboundFrame struct {
	envelope *localv1.Envelope
	written  chan error
}

func newServerSession(server *Server, connection *net.UnixConn) *serverSession {
	return &serverSession{
		server: server, conn: connection,
		out: make(chan outboundFrame, server.config.EventQueue), done: make(chan struct{}),
	}
}

func (session *serverSession) run(ctx context.Context) {
	defer session.server.wait.Done()
	var countryCancel context.CancelFunc
	var countryDone chan struct{}
	stopCountry := func() {
		if countryCancel != nil {
			countryCancel()
			<-countryDone
			countryCancel = nil
		}
	}
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		session.writeLoop()
	}()
	defer func() {
		session.close()
		stopCountry()
		session.logWait.Wait()
		<-writerDone
		session.server.unregister(session)
	}()
	helloComplete := false
	for {
		envelope, err := readFrameWithDeadline(session.conn, session.server.config.IdleTimeout, session.server.config.FrameTimeout)
		if err != nil {
			return
		}
		request := envelope.GetRequest()
		if request == nil || envelope.RequestId == 0 {
			session.enqueueAndWait(responseEnvelope(envelope.RequestId, localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "expected a request with nonzero request_id"))
			return
		}
		if !helloComplete {
			if request.Method != localv1.Method_METHOD_HELLO {
				session.enqueueAndWait(responseEnvelope(envelope.RequestId, localv1.ResultCode_RESULT_CODE_UNSUPPORTED_API, "HELLO must be the first request"))
				return
			}
			if envelope.ApiMajor != APIMajor {
				session.enqueueAndWait(responseEnvelope(envelope.RequestId, localv1.ResultCode_RESULT_CODE_UNSUPPORTED_API, "local API major version is unsupported"))
				return
			}
			negotiatedMinor := envelope.ApiMinor
			if negotiatedMinor > APIMinor {
				negotiatedMinor = APIMinor
			}
			session.negotiatedMinor = negotiatedMinor
			result := response(localv1.ResultCode_RESULT_CODE_OK, "")
			result.Payload = &localv1.Response_Hello{Hello: &localv1.Hello{
				ServerApiMajor: APIMajor, ServerApiMinor: APIMinor, NegotiatedApiMinor: negotiatedMinor,
				ProductVersion: session.server.config.ProductVersion, CoreVersion: CoreVersion,
			}}
			session.enqueue(&localv1.Envelope{
				ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: envelope.RequestId,
				Payload: &localv1.Envelope_Response{Response: result},
			})
			helloComplete = true
			continue
		}
		if envelope.ApiMajor != APIMajor {
			session.enqueueAndWait(responseEnvelope(envelope.RequestId, localv1.ResultCode_RESULT_CODE_UNSUPPORTED_API, "local API major version changed"))
			return
		}
		if minimum := requiredMinor(request.Method); minimum > session.negotiatedMinor {
			session.enqueue(&localv1.Envelope{
				ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: envelope.RequestId,
				Payload: &localv1.Envelope_Response{Response: response(
					localv1.ResultCode_RESULT_CODE_UNSUPPORTED,
					fmt.Sprintf("this method requires IRVIO Local API v1.%d", minimum),
				)},
			})
			continue
		}
		if request.Method == localv1.Method_METHOD_SUBSCRIBE_EVENTS {
			result := response(localv1.ResultCode_RESULT_CODE_OK, "")
			result.Accepted = true
			session.enqueue(&localv1.Envelope{
				ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: envelope.RequestId,
				Payload: &localv1.Envelope_Response{Response: result},
			})
			session.enqueue(session.server.subscribe(session))
			continue
		}
		if request.Method == localv1.Method_METHOD_SUBSCRIBE_LOGS {
			if session.server.config.Logs == nil {
				session.enqueue(&localv1.Envelope{
					ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: envelope.RequestId,
					Payload: &localv1.Envelope_Response{Response: response(localv1.ResultCode_RESULT_CODE_UNSUPPORTED, "product logs are unavailable")},
				})
				continue
			}
			subscribe := request.SubscribeLogs
			if subscribe == nil {
				session.enqueue(&localv1.Envelope{
					ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: envelope.RequestId,
					Payload: &localv1.Envelope_Response{Response: response(localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT, "log cursor is missing")},
				})
				continue
			}
			tail, updates, cancel := session.server.config.Logs.Subscribe(subscribe.DaemonIncarnation, subscribe.AfterSequence)
			if !session.setLogCancel(cancel) {
				cancel()
				session.enqueue(&localv1.Envelope{
					ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: envelope.RequestId,
					Payload: &localv1.Envelope_Response{Response: response(localv1.ResultCode_RESULT_CODE_BUSY, "log subscription already exists")},
				})
				continue
			}
			result := response(localv1.ResultCode_RESULT_CODE_OK, "")
			result.Accepted = true
			result.Payload = &localv1.Response_LogTail{LogTail: tail}
			if !session.enqueue(&localv1.Envelope{
				ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: envelope.RequestId,
				Payload: &localv1.Envelope_Response{Response: result},
			}) {
				return
			}
			session.logWait.Add(1)
			go session.forwardLogs(tail.DaemonIncarnation, updates)
			continue
		}
		if request.Method == localv1.Method_METHOD_GET_AVAILABLE_COUNTRIES {
			if countryCancel != nil {
				select {
				case <-countryDone:
					countryCancel()
					countryCancel = nil
				default:
					session.enqueue(responseEnvelope(envelope.RequestId, localv1.ResultCode_RESULT_CODE_BUSY, "country discovery already in progress"))
					continue
				}
			}
			queryCtx, cancel := context.WithCancel(ctx)
			countryCancel, countryDone = cancel, make(chan struct{})
			go func(requestID uint64, request *localv1.Request, done chan struct{}) {
				defer close(done)
				result := session.server.handleContext(queryCtx, request)
				session.enqueue(&localv1.Envelope{ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: requestID,
					Payload: &localv1.Envelope_Response{Response: result}})
			}(envelope.RequestId, request, countryDone)
			continue
		}
		switch request.Method {
		case localv1.Method_METHOD_CONNECT, localv1.Method_METHOD_DISCONNECT, localv1.Method_METHOD_SHUTDOWN,
			localv1.Method_METHOD_DELETE_PROFILE, localv1.Method_METHOD_IMPORT_BOOTSTRAP_BUNDLE:
			stopCountry()
		}
		result := session.server.handle(request)
		resultEnvelope := &localv1.Envelope{
			ApiMajor: APIMajor, ApiMinor: APIMinor, RequestId: envelope.RequestId,
			Payload: &localv1.Envelope_Response{Response: result},
		}
		if request.Method == localv1.Method_METHOD_DELETE_PROFILE && result.Accepted {
			if !session.enqueueAndWait(resultEnvelope) {
				session.server.config.Profile.CancelDelete()
				return
			}
			session.server.config.Profile.CommitDelete()
			return
		}
		if request.Method == localv1.Method_METHOD_SHUTDOWN && result.Accepted {
			session.enqueueAndWait(resultEnvelope)
			session.server.config.Shutdown.CommitShutdown()
			return
		}
		session.enqueue(resultEnvelope)
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func requiredMinor(method localv1.Method) uint32 {
	switch {
	case method == localv1.Method_METHOD_GET_TRAFFIC_STATS:
		return 5
	case method == localv1.Method_METHOD_GET_AVAILABLE_COUNTRIES:
		return 3
	case method == localv1.Method_METHOD_SHUTDOWN:
		return 4
	case method == localv1.Method_METHOD_DELETE_PROFILE:
		return 2
	case method >= localv1.Method_METHOD_GET_RECENT_LOGS && method <= localv1.Method_METHOD_GENERATE_BOOTSTRAP_BUNDLE:
		return 1
	default:
		return 0
	}
}

func availableCountriesToProto(value rendezvous.AvailableCountries) *localv1.AvailableCountries {
	status := localv1.AvailableCountriesStatus_AVAILABLE_COUNTRIES_STATUS_UNAVAILABLE
	switch value.Status {
	case rendezvous.AvailableCountriesReady:
		status = localv1.AvailableCountriesStatus_AVAILABLE_COUNTRIES_STATUS_READY
	case rendezvous.AvailableCountriesPartial:
		status = localv1.AvailableCountriesStatus_AVAILABLE_COUNTRIES_STATUS_PARTIAL
	case rendezvous.AvailableCountriesEmpty:
		status = localv1.AvailableCountriesStatus_AVAILABLE_COUNTRIES_STATUS_EMPTY
	}
	generatedAt := uint64(0)
	if !value.GeneratedAt.IsZero() && value.GeneratedAt.UnixMilli() > 0 {
		generatedAt = uint64(value.GeneratedAt.UnixMilli())
	}
	seen := make(map[string]struct{}, min(len(value.Codes), rendezvous.MaxAvailableCountries))
	codes := make([]string, 0, len(seen))
	for _, value := range value.Codes {
		country, valid := validCountry(value)
		if !valid || len(seen) >= rendezvous.MaxAvailableCountries {
			continue
		}
		if _, duplicate := seen[country]; duplicate {
			continue
		}
		seen[country] = struct{}{}
		codes = append(codes, country)
	}
	sort.Strings(codes)
	return &localv1.AvailableCountries{Status: status, CountryCodes: codes, GeneratedAtUnixMs: generatedAt}
}

func (session *serverSession) setLogCancel(cancel func()) bool {
	session.logMu.Lock()
	defer session.logMu.Unlock()
	if session.logCancel != nil {
		return false
	}
	select {
	case <-session.done:
		return false
	default:
	}
	session.logCancel = cancel
	return true
}

func (session *serverSession) forwardLogs(incarnation []byte, updates <-chan *localv1.LogRecord) {
	defer session.logWait.Done()
	for record := range updates {
		event := &localv1.Envelope{
			ApiMajor: APIMajor, ApiMinor: APIMinor,
			Payload: &localv1.Envelope_Event{Event: &localv1.Event{
				Sequence: record.Sequence,
				Payload: &localv1.Event_LogRecordAdded{LogRecordAdded: &localv1.LogRecordAdded{
					DaemonIncarnation: append([]byte(nil), incarnation...), Record: record,
				}},
			}},
		}
		if !session.enqueue(event) {
			session.close()
			return
		}
	}
	session.close()
}

func (session *serverSession) writeLoop() {
	defer session.close()
	for {
		select {
		case <-session.done:
			return
		case frame := <-session.out:
			envelope := frame.envelope
			if session.server.config.BeforeWriteFrame != nil {
				session.server.config.BeforeWriteFrame(envelope)
			}
			if err := session.conn.SetWriteDeadline(time.Now().Add(session.server.config.WriteTimeout)); err != nil {
				if frame.written != nil {
					frame.written <- err
				}
				return
			}
			if err := WriteFrame(session.conn, envelope); err != nil {
				if frame.written != nil {
					frame.written <- err
				}
				return
			}
			if frame.written != nil {
				frame.written <- nil
			}
		}
	}
}

func (session *serverSession) enqueue(envelope *localv1.Envelope) bool {
	select {
	case <-session.done:
		return false
	default:
	}
	select {
	case session.out <- outboundFrame{envelope: envelope}:
		return true
	default:
		return false
	}
}

func (session *serverSession) enqueueAndWait(envelope *localv1.Envelope) bool {
	written := make(chan error, 1)
	select {
	case <-session.done:
		return false
	case session.out <- outboundFrame{envelope: envelope, written: written}:
	}
	select {
	case <-session.done:
		return false
	case err := <-written:
		return err == nil
	}
}

func (session *serverSession) close() {
	session.once.Do(func() {
		session.logMu.Lock()
		cancel := session.logCancel
		session.logCancel = nil
		session.logMu.Unlock()
		if cancel != nil {
			cancel()
		}
		close(session.done)
		_ = session.conn.Close()
	})
}
