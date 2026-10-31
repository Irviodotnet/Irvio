// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

type scriptOptions struct {
	mapping             MappingBehavior
	filtering           FilteringBehavior
	baselineTimeout     bool
	missingOther        bool
	missingMapped       bool
	missingOrigin       bool
	wrongOrigin         bool
	invalidOther        netip.AddrPort
	baselineMappedSplit bool
	mappingSecondSplit  bool
	mappingSecondFail   bool
	mappingThirdFail    bool
	firstLivenessFail   bool
	changeUnsupported   bool
	wrongChangedSource  bool
}

type scriptExchanger struct {
	mu sync.Mutex

	family  socketmux.AddressFamily
	primary netip.AddrPort
	other   netip.AddrPort
	local   netip.AddrPort
	mapped1 netip.AddrPort
	mapped2 netip.AddrPort
	mapped3 netip.AddrPort
	options scriptOptions

	requests       []stunprobe.BindingRequest
	regularPrimary int
}

func newIPv4Script(options scriptOptions) *scriptExchanger {
	return &scriptExchanger{
		family:  socketmux.IPv4,
		primary: netip.MustParseAddrPort("192.0.2.1:3478"),
		other:   netip.MustParseAddrPort("192.0.2.2:3479"),
		local:   netip.MustParseAddrPort("10.0.0.2:41000"),
		mapped1: netip.MustParseAddrPort("198.51.100.8:51000"),
		mapped2: netip.MustParseAddrPort("198.51.100.8:51001"),
		mapped3: netip.MustParseAddrPort("198.51.100.8:51002"),
		options: options,
	}
}

func newIPv6Script(options scriptOptions) *scriptExchanger {
	return &scriptExchanger{
		family:  socketmux.IPv6,
		primary: netip.MustParseAddrPort("[2001:db8::1]:3478"),
		other:   netip.MustParseAddrPort("[2001:db8::2]:3479"),
		local:   netip.MustParseAddrPort("[fd00::2]:41000"),
		mapped1: netip.MustParseAddrPort("[2001:db8:1::8]:51000"),
		mapped2: netip.MustParseAddrPort("[2001:db8:1::8]:51001"),
		mapped3: netip.MustParseAddrPort("[2001:db8:1::8]:51002"),
		options: options,
	}
}

func (s *scriptExchanger) Exchange(ctx context.Context, request stunprobe.BindingRequest) (stunprobe.BindingResponse, error) {
	if err := ctx.Err(); err != nil {
		return stunprobe.BindingResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, copyBindingRequest(request))

	if request.ChangeIP && request.ChangePort {
		if s.options.changeUnsupported {
			return stunprobe.BindingResponse{}, fmt.Errorf("scripted change request: %w", &stunprobe.BindingError{Code: stun.CodeUnknownAttribute})
		}
		if s.options.filtering != FilteringEndpointIndependent {
			return stunprobe.BindingResponse{}, stunprobe.ErrTimeout
		}
		source := s.other
		if s.options.wrongChangedSource {
			source = s.primary
		}
		return s.response(request, source, s.mapped1), nil
	}
	if request.ChangePort {
		if s.options.changeUnsupported {
			return stunprobe.BindingResponse{}, fmt.Errorf("scripted change request: %w", &stunprobe.BindingError{Code: stun.CodeUnknownAttribute})
		}
		if s.options.filtering == FilteringAddressAndPortDependent {
			return stunprobe.BindingResponse{}, stunprobe.ErrTimeout
		}
		return s.response(request, netip.AddrPortFrom(s.primary.Addr(), s.other.Port()), s.mapped1), nil
	}

	alternatePrimary := netip.AddrPortFrom(s.other.Addr(), s.primary.Port())
	switch request.Destination {
	case alternatePrimary:
		if s.options.mappingSecondFail {
			return stunprobe.BindingResponse{}, stunprobe.ErrTimeout
		}
		mapped := s.mapped1
		if s.options.mapping == MappingAddressDependent || s.options.mapping == MappingAddressAndPortDependent {
			mapped = s.mapped2
		}
		response := s.response(request, alternatePrimary, mapped)
		if s.options.mappingSecondSplit {
			response.MappedAddress = stunprobe.OptionalAddrPort{AddrPort: s.mapped3, Present: true}
		}
		return response, nil
	case s.other:
		if s.options.mappingThirdFail {
			return stunprobe.BindingResponse{}, stunprobe.ErrTimeout
		}
		mapped := s.mapped2
		if s.options.mapping == MappingAddressAndPortDependent {
			mapped = s.mapped3
		}
		return s.response(request, s.other, mapped), nil
	case s.primary:
		s.regularPrimary++
		if s.regularPrimary == 1 && s.options.baselineTimeout {
			return stunprobe.BindingResponse{}, stunprobe.ErrTimeout
		}
		if s.regularPrimary == 2 && s.options.firstLivenessFail {
			return stunprobe.BindingResponse{}, stunprobe.ErrTimeout
		}
		response := s.response(request, s.primary, s.mapped1)
		if s.regularPrimary == 1 {
			if s.options.missingMapped {
				response.MappedAddress = stunprobe.OptionalAddrPort{}
			}
			if s.options.missingOther {
				response.OtherAddress = stunprobe.OptionalAddrPort{}
			}
			if s.options.missingOrigin {
				response.ResponseOrigin = stunprobe.OptionalAddrPort{}
			}
			if s.options.wrongOrigin {
				response.ResponseOrigin = stunprobe.OptionalAddrPort{AddrPort: s.other, Present: true}
			}
			if s.options.invalidOther.IsValid() {
				response.OtherAddress = stunprobe.OptionalAddrPort{AddrPort: s.options.invalidOther, Present: true}
			}
			if s.options.baselineMappedSplit {
				response.MappedAddress = stunprobe.OptionalAddrPort{AddrPort: s.mapped2, Present: true}
			}
		}
		return response, nil
	default:
		return stunprobe.BindingResponse{}, fmt.Errorf("unexpected scripted destination %s", request.Destination)
	}
}

func (s *scriptExchanger) response(request stunprobe.BindingRequest, source, mapped netip.AddrPort) stunprobe.BindingResponse {
	return stunprobe.BindingResponse{
		Destination: request.Destination, ResponseSource: source, LocalEndpoint: s.local,
		MappedEndpoint: mapped,
		MappedAddress:  stunprobe.OptionalAddrPort{AddrPort: mapped, Present: true},
		OtherAddress:   stunprobe.OptionalAddrPort{AddrPort: s.other, Present: true},
		ResponseOrigin: stunprobe.OptionalAddrPort{AddrPort: source, Present: true},
		Family:         s.family, Attempts: 1,
	}
}

func (s *scriptExchanger) requestSnapshot() []stunprobe.BindingRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]stunprobe.BindingRequest, len(s.requests))
	for index, request := range s.requests {
		requests[index] = copyBindingRequest(request)
	}
	return requests
}

func copyBindingRequest(request stunprobe.BindingRequest) stunprobe.BindingRequest {
	request.AllowedErrorSources = append([]netip.AddrPort(nil), request.AllowedErrorSources...)
	return request
}

func fastProfilerConfig() Config {
	return Config{MinimumTransactionInterval: 0, MaxObservations: defaultObservationLimit}
}

type familyExchanger struct {
	ipv4 BindingExchanger
	ipv6 BindingExchanger
}

func (e *familyExchanger) Exchange(ctx context.Context, request stunprobe.BindingRequest) (stunprobe.BindingResponse, error) {
	if request.Destination.Addr().Is4() {
		return e.ipv4.Exchange(ctx, request)
	}
	return e.ipv6.Exchange(ctx, request)
}

type gatedExchanger struct {
	delegate BindingExchanger
	started  chan socketmux.AddressFamily
	release  chan struct{}
}

func (e *gatedExchanger) Exchange(ctx context.Context, request stunprobe.BindingRequest) (stunprobe.BindingResponse, error) {
	select {
	case e.started <- endpointFamily(request.Destination):
	case <-ctx.Done():
		return stunprobe.BindingResponse{}, ctx.Err()
	}
	select {
	case <-e.release:
		return e.delegate.Exchange(ctx, request)
	case <-ctx.Done():
		return stunprobe.BindingResponse{}, ctx.Err()
	}
}
