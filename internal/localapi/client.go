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
	"net"
	"sync"
	"sync/atomic"
	"time"

	localv1 "hydra.local/hydra/proto/hydra/local/v1"
)

const maxPendingRequests = 16

const defaultClientWriteTimeout = 5 * time.Second

var (
	ErrClientClosed      = errors.New("local API client is closed")
	ErrTooManyRequests   = errors.New("too many pending local API requests")
	ErrUnexpectedMessage = errors.New("unexpected local API message")
)

type ResponseError struct {
	Code    localv1.ResultCode
	Message string
}

func (err *ResponseError) Error() string {
	if err.Message == "" {
		return err.Code.String()
	}
	return err.Message
}

type callResult struct {
	response *localv1.Response
	err      error
}

type Client struct {
	conn  *net.UnixConn
	major uint32
	minor uint32

	writeMu  sync.Mutex
	eventsMu sync.RWMutex
	mu       sync.Mutex
	pending  map[uint64]chan callResult
	events   chan *localv1.Event
	done     chan struct{}
	once     sync.Once
	nextID   atomic.Uint64
	err      error
	hello    *localv1.Hello
}

func Dial(ctx context.Context, socketPath string) (*Client, error) {
	return DialVersion(ctx, socketPath, APIMajor, APIMinor)
}

func DialVersion(ctx context.Context, socketPath string, major, minor uint32) (*Client, error) {
	if ctx == nil || socketPath == "" || major == 0 {
		return nil, ErrInvalidConfig
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		_ = connection.Close()
		return nil, errors.New("local API connection is not Unix")
	}
	client := &Client{
		conn: unixConnection, major: major, minor: minor,
		pending: make(map[uint64]chan callResult), events: make(chan *localv1.Event, 64), done: make(chan struct{}),
	}
	go client.readLoop()
	response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_HELLO})
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	client.hello = response.GetHello()
	if client.hello == nil || client.hello.ServerApiMajor != major ||
		client.hello.NegotiatedApiMinor > minor || client.hello.NegotiatedApiMinor > client.hello.ServerApiMinor {
		_ = client.Close()
		return nil, ErrUnexpectedMessage
	}
	client.minor = client.hello.NegotiatedApiMinor
	return client, nil
}

func (client *Client) Hello() *localv1.Hello { return client.hello }

func (client *Client) SupportsV11() bool {
	return client.hello != nil && client.hello.NegotiatedApiMinor >= 1
}

func (client *Client) SupportsV12() bool {
	return client.hello != nil && client.hello.NegotiatedApiMinor >= 2
}

func (client *Client) SupportsV13() bool {
	return client.hello != nil && client.hello.NegotiatedApiMinor >= 3
}

func (client *Client) GetStatus(ctx context.Context) (*localv1.Status, error) {
	response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_GET_STATUS})
	if err != nil {
		return nil, err
	}
	status := response.GetStatus()
	if status == nil {
		return nil, ErrUnexpectedMessage
	}
	return status, nil
}

func (client *Client) GetTrafficStats(ctx context.Context) (*localv1.TrafficStats, error) {
	response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_GET_TRAFFIC_STATS})
	if err != nil {
		return nil, err
	}
	stats := response.GetTrafficStats()
	if stats == nil {
		return nil, ErrUnexpectedMessage
	}
	return stats, nil
}

func (client *Client) Connect(ctx context.Context, country string) error {
	response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_CONNECT, CountryCode: country})
	if err != nil {
		return err
	}
	if !response.Accepted {
		return ErrUnexpectedMessage
	}
	return nil
}

func (client *Client) Disconnect(ctx context.Context) error {
	response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_DISCONNECT})
	if err != nil {
		return err
	}
	if !response.Accepted {
		return ErrUnexpectedMessage
	}
	return nil
}

func (client *Client) Shutdown(ctx context.Context) error {
	if client.minor < 4 {
		return &ResponseError{Code: localv1.ResultCode_RESULT_CODE_UNSUPPORTED, Message: "IRVIO Local API v1.4 is unavailable"}
	}
	response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_SHUTDOWN})
	if err != nil {
		return err
	}
	if !response.Accepted {
		return ErrUnexpectedMessage
	}
	return nil
}

func (client *Client) GetContribution(ctx context.Context) (*localv1.Contribution, error) {
	response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_GET_CONTRIBUTION})
	if err != nil {
		return nil, err
	}
	contribution := response.GetContribution()
	if contribution == nil {
		return nil, ErrUnexpectedMessage
	}
	return contribution, nil
}

func (client *Client) SetContribution(ctx context.Context, enabled bool) error {
	response, err := client.call(ctx, &localv1.Request{
		Method: localv1.Method_METHOD_SET_CONTRIBUTION, ContributionEnabled: enabled,
	})
	if err != nil {
		return err
	}
	if !response.Accepted {
		return ErrUnexpectedMessage
	}
	return nil
}

func (client *Client) SubscribeEvents(ctx context.Context) (<-chan *localv1.Event, error) {
	response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_SUBSCRIBE_EVENTS})
	if err != nil {
		return nil, err
	}
	if !response.Accepted {
		return nil, ErrUnexpectedMessage
	}
	return client.events, nil
}

func (client *Client) GetRecentLogs(ctx context.Context, limit uint32) (*localv1.LogTail, error) {
	if err := client.requireV11(); err != nil {
		return nil, err
	}
	response, err := client.call(ctx, &localv1.Request{
		Method:        localv1.Method_METHOD_GET_RECENT_LOGS,
		GetRecentLogs: &localv1.GetRecentLogsRequest{Limit: limit},
	})
	if err != nil {
		return nil, err
	}
	tail := response.GetLogTail()
	if tail == nil {
		return nil, ErrUnexpectedMessage
	}
	return tail, nil
}

func (client *Client) SubscribeLogs(ctx context.Context, incarnation []byte, after uint64) (*localv1.LogTail, <-chan *localv1.Event, error) {
	if err := client.requireV11(); err != nil {
		return nil, nil, err
	}
	response, err := client.call(ctx, &localv1.Request{
		Method: localv1.Method_METHOD_SUBSCRIBE_LOGS,
		SubscribeLogs: &localv1.SubscribeLogsRequest{
			DaemonIncarnation: append([]byte(nil), incarnation...), AfterSequence: after,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	tail := response.GetLogTail()
	if !response.Accepted || tail == nil {
		return nil, nil, ErrUnexpectedMessage
	}
	return tail, client.events, nil
}

func (client *Client) GetBootstrapState(ctx context.Context) (*localv1.BootstrapState, error) {
	if err := client.requireV11(); err != nil {
		return nil, err
	}
	response, err := client.call(ctx, &localv1.Request{Method: localv1.Method_METHOD_GET_BOOTSTRAP_STATE})
	if err != nil {
		return nil, err
	}
	state := response.GetBootstrapState()
	if state == nil {
		return nil, ErrUnexpectedMessage
	}
	return state, nil
}

func (client *Client) ImportBootstrapBundle(ctx context.Context, code string) (*localv1.BootstrapState, error) {
	if err := client.requireV11(); err != nil {
		return nil, err
	}
	response, err := client.call(ctx, &localv1.Request{
		Method:                localv1.Method_METHOD_IMPORT_BOOTSTRAP_BUNDLE,
		ImportBootstrapBundle: &localv1.ImportBootstrapBundleRequest{Code: code},
	})
	if err != nil {
		return nil, err
	}
	state := response.GetBootstrapState()
	if !response.Accepted || state == nil {
		return nil, ErrUnexpectedMessage
	}
	return state, nil
}

func (client *Client) GenerateBootstrapBundle(ctx context.Context, preferred uint32) (*localv1.GeneratedBootstrapBundle, error) {
	if err := client.requireV11(); err != nil {
		return nil, err
	}
	response, err := client.call(ctx, &localv1.Request{
		Method:                  localv1.Method_METHOD_GENERATE_BOOTSTRAP_BUNDLE,
		GenerateBootstrapBundle: &localv1.GenerateBootstrapBundleRequest{PreferredNodeCount: preferred},
	})
	if err != nil {
		return nil, err
	}
	bundle := response.GetGeneratedBootstrapBundle()
	if bundle == nil {
		return nil, ErrUnexpectedMessage
	}
	return bundle, nil
}

func (client *Client) DeleteProfile(ctx context.Context) error {
	if err := client.requireV12(); err != nil {
		return err
	}
	response, err := client.call(ctx, &localv1.Request{
		Method:        localv1.Method_METHOD_DELETE_PROFILE,
		DeleteProfile: &localv1.DeleteProfileRequest{Confirm: true},
	})
	if err != nil {
		return err
	}
	if !response.Accepted {
		return ErrUnexpectedMessage
	}
	return nil
}

func (client *Client) GetAvailableCountries(ctx context.Context, refresh bool) (*localv1.AvailableCountries, error) {
	if err := client.requireV13(); err != nil {
		return nil, err
	}
	response, err := client.call(ctx, &localv1.Request{
		Method:                localv1.Method_METHOD_GET_AVAILABLE_COUNTRIES,
		GetAvailableCountries: &localv1.GetAvailableCountriesRequest{Refresh: refresh},
	})
	if err != nil {
		return nil, err
	}
	available := response.GetAvailableCountries()
	if available == nil {
		return nil, ErrUnexpectedMessage
	}
	return available, nil
}

func (client *Client) requireV11() error {
	if client.SupportsV11() {
		return nil
	}
	return &ResponseError{Code: localv1.ResultCode_RESULT_CODE_UNSUPPORTED, Message: "IRVIO Local API v1.1 is unavailable"}
}

func (client *Client) requireV12() error {
	if client.SupportsV12() {
		return nil
	}
	return &ResponseError{Code: localv1.ResultCode_RESULT_CODE_UNSUPPORTED, Message: "IRVIO Local API v1.2 is unavailable"}
}

func (client *Client) requireV13() error {
	if client.SupportsV13() {
		return nil
	}
	return &ResponseError{Code: localv1.ResultCode_RESULT_CODE_UNSUPPORTED, Message: "IRVIO Local API v1.3 is unavailable"}
}

func (client *Client) Close() error {
	client.closeWithError(ErrClientClosed)
	return nil
}

func (client *Client) call(ctx context.Context, request *localv1.Request) (*localv1.Response, error) {
	if ctx == nil {
		return nil, ErrInvalidConfig
	}
	requestID := client.nextID.Add(1)
	result := make(chan callResult, 1)
	client.mu.Lock()
	select {
	case <-client.done:
		err := client.err
		client.mu.Unlock()
		if err == nil {
			err = ErrClientClosed
		}
		return nil, err
	default:
	}
	if len(client.pending) >= maxPendingRequests {
		client.mu.Unlock()
		return nil, ErrTooManyRequests
	}
	client.pending[requestID] = result
	client.mu.Unlock()
	envelope := &localv1.Envelope{
		ApiMajor: client.major, ApiMinor: client.minor, RequestId: requestID,
		Payload: &localv1.Envelope_Request{Request: request},
	}
	client.writeMu.Lock()
	writeDeadline := time.Now().Add(defaultClientWriteTimeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(writeDeadline) {
		writeDeadline = deadline
	}
	_ = client.conn.SetWriteDeadline(writeDeadline)
	err := WriteFrame(client.conn, envelope)
	_ = client.conn.SetWriteDeadline(time.Time{})
	client.writeMu.Unlock()
	if err != nil {
		client.removePending(requestID)
		client.closeWithError(err)
		return nil, err
	}
	select {
	case <-ctx.Done():
		client.removePending(requestID)
		return nil, ctx.Err()
	case <-client.done:
		client.mu.Lock()
		err := client.err
		client.mu.Unlock()
		if err == nil {
			err = ErrClientClosed
		}
		return nil, err
	case value := <-result:
		if value.err != nil {
			return nil, value.err
		}
		if value.response.Code != localv1.ResultCode_RESULT_CODE_OK {
			return nil, &ResponseError{Code: value.response.Code, Message: value.response.UserMessage}
		}
		return value.response, nil
	}
}

func (client *Client) readLoop() {
	for {
		envelope, err := ReadFrame(client.conn)
		if err != nil {
			client.closeWithError(err)
			return
		}
		if response := envelope.GetResponse(); response != nil {
			client.mu.Lock()
			pending := client.pending[envelope.RequestId]
			delete(client.pending, envelope.RequestId)
			client.mu.Unlock()
			if pending == nil {
				continue
			}
			pending <- callResult{response: response}
			continue
		}
		if event := envelope.GetEvent(); event != nil && envelope.RequestId == 0 {
			client.eventsMu.RLock()
			select {
			case client.events <- event:
				client.eventsMu.RUnlock()
			default:
				client.eventsMu.RUnlock()
				client.closeWithError(errors.New("local API event consumer is too slow"))
				return
			}
			continue
		}
		client.closeWithError(ErrUnexpectedMessage)
		return
	}
}

func (client *Client) removePending(requestID uint64) {
	client.mu.Lock()
	delete(client.pending, requestID)
	client.mu.Unlock()
}

func (client *Client) closeWithError(err error) {
	client.once.Do(func() {
		_ = client.conn.Close()
		client.mu.Lock()
		client.err = err
		pending := client.pending
		client.pending = make(map[uint64]chan callResult)
		close(client.done)
		client.mu.Unlock()
		for _, waiter := range pending {
			waiter <- callResult{err: err}
		}
		client.eventsMu.Lock()
		close(client.events)
		client.eventsMu.Unlock()
	})
}
