// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/stun/v3"

	"hydra.local/hydra/internal/socketmux"
)

const transactionIDAttempts = 8

// Client performs concurrent Basic STUN Binding probes through one Control endpoint.
type Client struct {
	endpoint *socketmux.Endpoint
	registry *Registry
	config   Config

	context      context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	closed       atomic.Bool
	shutdownOnce sync.Once
	detachOnce   sync.Once
	dispatcher   sync.WaitGroup
	lifecycleMu  sync.RWMutex
	reasonMu     sync.Mutex
	shutdownErr  error
	stats        clientCounters
}

// NewClient starts the sole dispatcher that reads the logical Control endpoint.
func NewClient(endpoint *socketmux.Endpoint, registry *Registry, config Config) (*Client, error) {
	return newClient(endpoint, registry, config, true)
}

// NewClientWithExternalDispatcher uses the existing exclusive Control reader.
func NewClientWithExternalDispatcher(endpoint *socketmux.Endpoint, registry *Registry, config Config) (*Client, error) {
	return newClient(endpoint, registry, config, false)
}

func newClient(endpoint *socketmux.Endpoint, registry *Registry, config Config, startDispatcher bool) (*Client, error) {
	if endpoint == nil {
		return nil, fmt.Errorf("create STUN client: nil Control endpoint")
	}
	if registry == nil {
		return nil, fmt.Errorf("create STUN client: nil transaction registry")
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("validate STUN client config: %w", err)
	}
	if err := registry.attach(); err != nil {
		return nil, fmt.Errorf("attach STUN dispatcher: %w", err)
	}
	clientContext, cancel := context.WithCancel(context.Background())
	client := &Client{
		endpoint: endpoint,
		registry: registry,
		config:   config,
		context:  clientContext,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	if startDispatcher {
		client.dispatcher.Add(1)
		go client.dispatch()
	}
	return client, nil
}

// HandleResponse reports whether an externally dispatched datagram completed an active probe.
func (c *Client) HandleResponse(datagram socketmux.Datagram) bool {
	handled, _ := c.handleResponse(datagram)
	return handled
}

// Probe performs one transaction-aware Basic STUN Binding probe.
func (c *Client) Probe(ctx context.Context, server netip.AddrPort) (Result, error) {
	response, err := c.Exchange(ctx, BindingRequest{
		Destination:           server,
		ExpectedSuccessSource: server,
		AllowedErrorSources:   []netip.AddrPort{server},
	})
	if err != nil {
		return Result{}, err
	}
	return Result{
		Server: response.Destination, Local: response.LocalEndpoint, Mapped: response.MappedEndpoint,
		Family: response.Family, RoundTrip: response.RoundTrip, Attempts: response.Attempts,
		CompletedAt: response.CompletedAt,
	}, nil
}

// Exchange performs one Binding transaction with an explicit bounded response-source policy.
func (c *Client) Exchange(ctx context.Context, requestSpec BindingRequest) (BindingResponse, error) {
	if ctx == nil {
		return BindingResponse{}, fmt.Errorf("exchange STUN Binding request: nil context")
	}
	if err := ctx.Err(); err != nil {
		return BindingResponse{}, err
	}
	normalized, policy, family, err := normalizeBindingRequest(requestSpec)
	if err != nil {
		return BindingResponse{}, err
	}
	local, active := c.endpoint.LocalAddr(family)
	if !active {
		return BindingResponse{}, fmt.Errorf("%w: %s", socketmux.ErrFamilyUnavailable, family.String())
	}
	c.stats.probesStarted.Add(1)
	started := time.Now()

	probeContext, cancel := context.WithCancel(ctx)
	stopClientCancel := context.AfterFunc(c.context, cancel)
	defer func() {
		stopClientCancel()
		cancel()
	}()

	request, tx, err := c.buildAndRegister(normalized, policy, family, started)
	if err != nil {
		if errors.Is(err, ErrInFlightLimit) {
			c.stats.probesRejectedInFlight.Add(1)
		}
		return BindingResponse{}, fmt.Errorf("register STUN Binding transaction: %w", err)
	}

	attempts := 0
	rto := c.config.InitialRTO
	for {
		attempts++
		if err := c.transmit(probeContext, tx, request.Raw, normalized.Destination, attempts); err != nil {
			c.registry.retire(tx, nil, time.Now())
			if errors.Is(err, ErrTransportClosed) {
				c.shutdown(ErrTransportClosed)
			}
			return BindingResponse{}, c.probeError(ctx, err)
		}

		timer := time.NewTimer(rto)
		select {
		case event := <-tx.delivery:
			timer.Stop()
			return c.finishResponse(event, tx, normalized, local, family, started, attempts)
		case <-ctx.Done():
			timer.Stop()
			c.registry.retire(tx, nil, time.Now())
			c.stats.probesCanceled.Add(1)
			return BindingResponse{}, ctx.Err()
		case <-c.done:
			timer.Stop()
			c.registry.retire(tx, nil, time.Now())
			return BindingResponse{}, fmt.Errorf("exchange STUN Binding request with %s: %w", normalized.Destination, c.closedError())
		case <-timer.C:
			select {
			case event := <-tx.delivery:
				return c.finishResponse(event, tx, normalized, local, family, started, attempts)
			default:
			}
			if attempts >= c.config.MaxAttempts {
				if !c.registry.retire(tx, nil, time.Now()) {
					select {
					case event := <-tx.delivery:
						return c.finishResponse(event, tx, normalized, local, family, started, attempts)
					case <-ctx.Done():
						c.stats.probesCanceled.Add(1)
						return BindingResponse{}, ctx.Err()
					case <-c.done:
						return BindingResponse{}, fmt.Errorf("exchange STUN Binding request with %s: %w", normalized.Destination, c.closedError())
					}
				}
				c.stats.probesTimedOut.Add(1)
				return BindingResponse{}, fmt.Errorf("exchange STUN Binding request with %s after %d attempts: %w", normalized.Destination, attempts, ErrTimeout)
			}
			if rto >= c.config.MaxRTO/2 {
				rto = c.config.MaxRTO
			} else {
				rto *= 2
			}
		}
	}
}

func (c *Client) finishResponse(event responseEvent, tx *transaction, request BindingRequest, local netip.AddrPort, family socketmux.AddressFamily, started time.Time, attempts int) (BindingResponse, error) {
	if event.err != nil {
		return BindingResponse{}, fmt.Errorf("exchange STUN Binding request with %s: %w", request.Destination, event.err)
	}
	result, err := c.resultFromResponse(event, tx, request, local, family, started, attempts)
	if err != nil {
		return BindingResponse{}, err
	}
	c.stats.probesSucceeded.Add(1)
	return result, nil
}

func (c *Client) buildAndRegister(requestSpec BindingRequest, policy responseSourcePolicy, family socketmux.AddressFamily, now time.Time) (*stun.Message, *transaction, error) {
	for attempt := 0; attempt < transactionIDAttempts; attempt++ {
		request, err := stun.Build(stun.TransactionID, stun.BindingRequest)
		if err != nil {
			return nil, nil, fmt.Errorf("build Pion Binding request: %w", err)
		}
		addChangeRequest(request, requestSpec.ChangeIP, requestSpec.ChangePort)
		c.lifecycleMu.RLock()
		if c.closed.Load() {
			c.lifecycleMu.RUnlock()
			return nil, nil, c.closedError()
		}
		tx, registerErr := c.registry.register(transactionID(request.TransactionID), policy, family, now)
		c.lifecycleMu.RUnlock()
		if registerErr == nil {
			return request, tx, nil
		}
		if !errors.Is(registerErr, ErrDuplicateTransaction) {
			return nil, nil, registerErr
		}
	}
	return nil, nil, ErrDuplicateTransaction
}

func (c *Client) transmit(ctx context.Context, tx *transaction, payload []byte, server netip.AddrPort, attempt int) error {
	c.lifecycleMu.RLock()
	defer c.lifecycleMu.RUnlock()
	if c.closed.Load() {
		return c.closedError()
	}
	if _, err := c.endpoint.WriteTo(ctx, payload, server); err != nil {
		if errors.Is(err, socketmux.ErrClosed) {
			return ErrTransportClosed
		}
		return fmt.Errorf("send Binding request: %w", err)
	}
	c.stats.requestsTransmitted.Add(1)
	if attempt > 1 {
		c.stats.retransmissions.Add(1)
	}
	return nil
}

func (c *Client) dispatch() {
	defer c.dispatcher.Done()
	for {
		datagram, err := c.endpoint.Read(c.context)
		if err != nil {
			if c.closed.Load() || errors.Is(err, context.Canceled) {
				return
			}
			c.stats.dispatcherErrors.Add(1)
			if errors.Is(err, socketmux.ErrClosed) {
				c.shutdown(ErrTransportClosed)
			} else {
				c.shutdown(fmt.Errorf("STUN dispatcher read: %w", err))
			}
			return
		}
		_, decoded := c.handleResponse(datagram)
		if !decoded {
			c.stats.dispatcherErrors.Add(1)
		}
	}
}

func (c *Client) handleResponse(datagram socketmux.Datagram) (bool, bool) {
	if c == nil || c.closed.Load() {
		return false, false
	}
	message, status := decodeResponse(datagram.Payload)
	if status != decodeBindingResponse {
		return false, false
	}
	handled := c.registry.complete(transactionID(message.TransactionID), datagram.Source, datagram.Family, message, time.Now())
	if handled {
		c.stats.validResponsesDispatched.Add(1)
	}
	return handled, true
}

func (c *Client) resultFromResponse(event responseEvent, tx *transaction, request BindingRequest, local netip.AddrPort, family socketmux.AddressFamily, started time.Time, attempts int) (BindingResponse, error) {
	message := event.message
	if message == nil || transactionID(message.TransactionID) != tx.id {
		return BindingResponse{}, fmt.Errorf("%w: transaction ID mismatch", ErrInvalidResponse)
	}
	if !tx.policy.matches(event.source, message.Type.Class) || event.family != family {
		return BindingResponse{}, fmt.Errorf("%w: response source or family mismatch", ErrInvalidResponse)
	}
	if message.Type.Method != stun.MethodBinding {
		return BindingResponse{}, fmt.Errorf("%w: unexpected method %s", ErrInvalidResponse, message.Type.Method)
	}
	if message.Type.Class == stun.ClassErrorResponse {
		var errorAttribute stun.ErrorCodeAttribute
		if err := errorAttribute.GetFrom(message); err != nil {
			return BindingResponse{}, fmt.Errorf("%w: decode ERROR-CODE: %w", ErrInvalidResponse, err)
		}
		reason := errorAttribute.Reason
		if len(reason) > maximumErrorReasonLength {
			reason = reason[:maximumErrorReasonLength]
		}
		bindingErr := &BindingError{Code: errorAttribute.Code, Reason: string(append([]byte(nil), reason...))}
		return BindingResponse{}, fmt.Errorf("exchange STUN Binding request with %s: %w", request.Destination, bindingErr)
	}
	if message.Type.Class != stun.ClassSuccessResponse {
		return BindingResponse{}, fmt.Errorf("%w: unexpected class %d", ErrInvalidResponse, message.Type.Class)
	}
	var mappedAttribute stun.XORMappedAddress
	if err := mappedAttribute.GetFrom(message); err != nil {
		return BindingResponse{}, fmt.Errorf("%w: decode XOR-MAPPED-ADDRESS: %w", ErrInvalidResponse, err)
	}
	mappedEndpoint, err := decodedEndpoint(mappedAttribute.IP, mappedAttribute.Port, family)
	if err != nil {
		return BindingResponse{}, fmt.Errorf("%w: invalid XOR-MAPPED-ADDRESS: %v", ErrInvalidResponse, err)
	}
	mappedAddress, err := optionalMappedAddress(message, family)
	if err != nil {
		return BindingResponse{}, fmt.Errorf("%w: decode MAPPED-ADDRESS: %v", ErrInvalidResponse, err)
	}
	otherAddress, err := optionalOtherAddress(message, family)
	if err != nil {
		return BindingResponse{}, fmt.Errorf("%w: decode OTHER-ADDRESS: %v", ErrInvalidResponse, err)
	}
	responseOrigin, err := optionalResponseOrigin(message, family)
	if err != nil {
		return BindingResponse{}, fmt.Errorf("%w: decode RESPONSE-ORIGIN: %v", ErrInvalidResponse, err)
	}
	completedAt := time.Now()
	return BindingResponse{
		Destination: request.Destination, ResponseSource: event.source, LocalEndpoint: local,
		MappedEndpoint: mappedEndpoint, MappedAddress: mappedAddress, OtherAddress: otherAddress,
		ResponseOrigin: responseOrigin, Family: family, RoundTrip: completedAt.Sub(started), Attempts: attempts,
		CompletedAt: completedAt, TransactionID: TransactionID(message.TransactionID),
	}, nil
}

func normalizeBindingRequest(request BindingRequest) (BindingRequest, responseSourcePolicy, socketmux.AddressFamily, error) {
	family, err := serverFamily(request.Destination)
	if err != nil {
		return BindingRequest{}, responseSourcePolicy{}, 0, fmt.Errorf("invalid Binding destination: %w", err)
	}
	expectedFamily, err := serverFamily(request.ExpectedSuccessSource)
	if err != nil || expectedFamily != family {
		return BindingRequest{}, responseSourcePolicy{}, 0, fmt.Errorf("%w: invalid expected success source", ErrInvalidSourcePolicy)
	}
	errorSources := request.AllowedErrorSources
	if len(errorSources) == 0 {
		errorSources = []netip.AddrPort{request.Destination}
	}
	if len(errorSources) > MaxResponseSources {
		return BindingRequest{}, responseSourcePolicy{}, 0, fmt.Errorf("%w: at most %d error sources are allowed", ErrInvalidSourcePolicy, MaxResponseSources)
	}
	policy := responseSourcePolicy{success: request.ExpectedSuccessSource}
	normalizedErrors := make([]netip.AddrPort, 0, len(errorSources))
	for _, source := range errorSources {
		sourceFamily, sourceErr := serverFamily(source)
		if sourceErr != nil || sourceFamily != family {
			return BindingRequest{}, responseSourcePolicy{}, 0, fmt.Errorf("%w: invalid error response source", ErrInvalidSourcePolicy)
		}
		duplicate := false
		for _, existing := range normalizedErrors {
			if source == existing {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		policy.errors[policy.errorCount] = source
		policy.errorCount++
		normalizedErrors = append(normalizedErrors, source)
	}
	request.AllowedErrorSources = normalizedErrors
	return request, policy, family, nil
}

func decodedEndpoint(ip []byte, port int, family socketmux.AddressFamily) (netip.AddrPort, error) {
	address, ok := netip.AddrFromSlice(ip)
	if !ok || !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() {
		return netip.AddrPort{}, fmt.Errorf("invalid address")
	}
	if port <= 0 || port > 65535 {
		return netip.AddrPort{}, fmt.Errorf("invalid port")
	}
	if (family == socketmux.IPv4 && !address.Is4()) || (family == socketmux.IPv6 && !address.Is6()) {
		return netip.AddrPort{}, fmt.Errorf("address family mismatch")
	}
	return netip.AddrPortFrom(address, uint16(port)), nil
}

func optionalMappedAddress(message *stun.Message, family socketmux.AddressFamily) (OptionalAddrPort, error) {
	var attribute stun.MappedAddress
	if err := attribute.GetFrom(message); err != nil {
		if errors.Is(err, stun.ErrAttributeNotFound) {
			return OptionalAddrPort{}, nil
		}
		return OptionalAddrPort{}, err
	}
	endpoint, err := decodedEndpoint(attribute.IP, attribute.Port, family)
	return OptionalAddrPort{AddrPort: endpoint, Present: err == nil}, err
}

func optionalOtherAddress(message *stun.Message, family socketmux.AddressFamily) (OptionalAddrPort, error) {
	var attribute stun.OtherAddress
	if err := attribute.GetFrom(message); err != nil {
		if errors.Is(err, stun.ErrAttributeNotFound) {
			return OptionalAddrPort{}, nil
		}
		return OptionalAddrPort{}, err
	}
	endpoint, err := decodedEndpoint(attribute.IP, attribute.Port, family)
	return OptionalAddrPort{AddrPort: endpoint, Present: err == nil}, err
}

func optionalResponseOrigin(message *stun.Message, family socketmux.AddressFamily) (OptionalAddrPort, error) {
	var attribute stun.ResponseOrigin
	if err := attribute.GetFrom(message); err != nil {
		if errors.Is(err, stun.ErrAttributeNotFound) {
			return OptionalAddrPort{}, nil
		}
		return OptionalAddrPort{}, err
	}
	endpoint, err := decodedEndpoint(attribute.IP, attribute.Port, family)
	return OptionalAddrPort{AddrPort: endpoint, Present: err == nil}, err
}

func serverFamily(server netip.AddrPort) (socketmux.AddressFamily, error) {
	if !server.IsValid() || server.Port() == 0 || server.Addr().IsUnspecified() || server.Addr().IsMulticast() || server.Addr().Is4In6() {
		return 0, ErrInvalidServer
	}
	if server.Addr().Is4() {
		return socketmux.IPv4, nil
	}
	if server.Addr().Is6() {
		return socketmux.IPv6, nil
	}
	return 0, ErrInvalidServer
}

func (c *Client) probeError(probeContext context.Context, err error) error {
	if contextErr := probeContext.Err(); contextErr != nil {
		c.stats.probesCanceled.Add(1)
		return contextErr
	}
	if c.closed.Load() {
		return c.closedError()
	}
	return err
}

func (c *Client) shutdown(reason error) {
	c.shutdownOnce.Do(func() {
		c.reasonMu.Lock()
		c.shutdownErr = reason
		c.reasonMu.Unlock()
		c.closed.Store(true)
		c.cancel()
		close(c.done)
		c.lifecycleMu.Lock()
		c.registry.retireAll(reason, time.Now())
		c.lifecycleMu.Unlock()
	})
}

func (c *Client) closedError() error {
	c.reasonMu.Lock()
	defer c.reasonMu.Unlock()
	if c.shutdownErr != nil {
		return c.shutdownErr
	}
	return ErrClosed
}

// Stats returns an atomic snapshot without blocking packet dispatch.
func (c *Client) Stats() Stats { return c.statsSnapshot() }

// Close stops probes and the dispatcher without closing IrvioSocketMux.
func (c *Client) Close() error {
	c.shutdown(ErrClosed)
	c.dispatcher.Wait()
	c.detachOnce.Do(c.registry.detach)
	return nil
}
