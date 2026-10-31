// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestMappingBehaviorClassification(t *testing.T) {
	for _, behavior := range []MappingBehavior{
		MappingEndpointIndependent,
		MappingAddressDependent,
		MappingAddressAndPortDependent,
	} {
		t.Run(mappingName(behavior), func(t *testing.T) {
			script := newIPv4Script(scriptOptions{mapping: behavior, filtering: FilteringEndpointIndependent})
			profiler, err := New(script, fastProfilerConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer profiler.Close()
			profile, err := profiler.Profile(context.Background(), script.primary)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Mapping != behavior || profile.Filtering != FilteringEndpointIndependent || profile.Status != ProfileComplete || profile.Translation != TranslationPresent {
				t.Fatalf("unexpected profile: %+v", profile)
			}
			assertFilteringBeforeMapping(t, script)
		})
	}
}

func TestIPv6AlgorithmMatrix(t *testing.T) {
	tests := []struct {
		mapping   MappingBehavior
		filtering FilteringBehavior
	}{
		{MappingEndpointIndependent, FilteringEndpointIndependent},
		{MappingAddressDependent, FilteringAddressDependent},
		{MappingAddressAndPortDependent, FilteringAddressAndPortDependent},
	}
	for _, test := range tests {
		name := mappingName(test.mapping) + "/" + filteringName(test.filtering)
		t.Run(name, func(t *testing.T) {
			script := newIPv6Script(scriptOptions{mapping: test.mapping, filtering: test.filtering})
			profiler, err := New(script, fastProfilerConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer profiler.Close()
			profile, err := profiler.Profile(context.Background(), script.primary)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Family != socketmux.IPv6 || profile.Mapping != test.mapping || profile.Filtering != test.filtering || profile.Status != ProfileComplete {
				t.Fatalf("unexpected IPv6 profile: %+v", profile)
			}
		})
	}
}

func TestMappingFailuresRemainUnknown(t *testing.T) {
	for name, options := range map[string]scriptOptions{
		"Test II timeout":  {mapping: MappingAddressDependent, filtering: FilteringEndpointIndependent, mappingSecondFail: true},
		"Test III timeout": {mapping: MappingAddressDependent, filtering: FilteringEndpointIndependent, mappingThirdFail: true},
		"mapped mismatch":  {mapping: MappingEndpointIndependent, filtering: FilteringEndpointIndependent, mappingSecondSplit: true},
	} {
		t.Run(name, func(t *testing.T) {
			script := newIPv4Script(options)
			profiler, err := New(script, fastProfilerConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer profiler.Close()
			profile, err := profiler.Profile(context.Background(), script.primary)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Mapping != MappingUnknown || profile.Filtering != FilteringEndpointIndependent || profile.Status != ProfilePartial {
				t.Fatalf("mapping failure was classified: %+v", profile)
			}
		})
	}
}

func TestFilteringBehaviorClassificationAndLiveness(t *testing.T) {
	for _, behavior := range []FilteringBehavior{
		FilteringEndpointIndependent,
		FilteringAddressDependent,
		FilteringAddressAndPortDependent,
	} {
		t.Run(filteringName(behavior), func(t *testing.T) {
			script := newIPv4Script(scriptOptions{mapping: MappingEndpointIndependent, filtering: behavior})
			profiler, err := New(script, fastProfilerConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer profiler.Close()
			profile, err := profiler.Profile(context.Background(), script.primary)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Filtering != behavior || profile.Mapping != MappingEndpointIndependent || profile.Status != ProfileComplete {
				t.Fatalf("unexpected profile: %+v", profile)
			}
			wantInferred := behavior != FilteringEndpointIndependent
			if profile.FilteringInferredFromTimeout != wantInferred {
				t.Fatalf("timeout inference = %v, want %v", profile.FilteringInferredFromTimeout, wantInferred)
			}
			assertFilteringBeforeMapping(t, script)
			assertRequiredLiveness(t, profile, behavior)
		})
	}
}

func TestProfilerStatsDescribeCompletedTimeoutDerivedProfile(t *testing.T) {
	script := newIPv4Script(scriptOptions{mapping: MappingAddressDependent, filtering: FilteringAddressAndPortDependent})
	profiler, err := New(script, fastProfilerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer profiler.Close()
	profile, err := profiler.Profile(context.Background(), script.primary)
	if err != nil || profile.Status != ProfileComplete {
		t.Fatalf("profile = %+v, %v", profile, err)
	}
	stats := profiler.Stats()
	if stats.ProfilesStarted != 1 || stats.ProfilesComplete != 1 || stats.Exchanges != 7 || stats.ExchangeTimeouts != 2 || stats.ExchangeProtocolErrors != 0 {
		t.Fatalf("unexpected profiler stats: %+v", stats)
	}
}

func TestFilteringFailureAndUnknownAttributeProducePartialProfile(t *testing.T) {
	for name, options := range map[string]scriptOptions{
		"failed liveness":   {mapping: MappingEndpointIndependent, filtering: FilteringAddressDependent, firstLivenessFail: true},
		"unknown attribute": {mapping: MappingEndpointIndependent, filtering: FilteringEndpointIndependent, changeUnsupported: true},
		"wrong source":      {mapping: MappingEndpointIndependent, filtering: FilteringEndpointIndependent, wrongChangedSource: true},
	} {
		t.Run(name, func(t *testing.T) {
			script := newIPv4Script(options)
			profiler, err := New(script, fastProfilerConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer profiler.Close()
			profile, err := profiler.Profile(context.Background(), script.primary)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Filtering != FilteringUnknown || profile.Mapping != MappingEndpointIndependent || profile.Status != ProfilePartial {
				t.Fatalf("unexpected partial profile: %+v", profile)
			}
			if name == "failed liveness" && profile.Reason != ReasonLivenessFailed {
				t.Fatalf("liveness reason = %v", profile.Reason)
			}
			if name == "unknown attribute" && profile.Reason != ReasonServerUnsupported {
				t.Fatalf("unsupported reason = %v", profile.Reason)
			}
		})
	}
}

func TestBaselineFailuresHavePreciseStatus(t *testing.T) {
	for name, options := range map[string]scriptOptions{
		"connectivity unconfirmed": {baselineTimeout: true},
		"missing OTHER-ADDRESS":    {missingOther: true},
		"missing MAPPED-ADDRESS":   {missingMapped: true},
		"missing RESPONSE-ORIGIN":  {missingOrigin: true},
		"wrong RESPONSE-ORIGIN":    {wrongOrigin: true},
		"mapped address mismatch":  {baselineMappedSplit: true},
	} {
		t.Run(name, func(t *testing.T) {
			script := newIPv4Script(options)
			profiler, err := New(script, fastProfilerConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer profiler.Close()
			profile, err := profiler.Profile(context.Background(), script.primary)
			if err != nil {
				t.Fatal(err)
			}
			if options.baselineTimeout {
				if profile.Status != ProfileConnectivityUnconfirmed {
					t.Fatalf("status = %v", profile.Status)
				}
			} else if options.missingOther || options.missingMapped || options.missingOrigin {
				if profile.Status != ProfileServerUnsupported {
					t.Fatalf("status = %v", profile.Status)
				}
			} else if profile.Status != ProfileInconclusive {
				t.Fatalf("status = %v", profile.Status)
			}
			if len(script.requestSnapshot()) != 1 {
				t.Fatalf("baseline failure continued profiling: %+v", script.requestSnapshot())
			}
		})
	}
}

func TestConcreteNoTranslationSkipsMappingPhase(t *testing.T) {
	script := newIPv4Script(scriptOptions{mapping: MappingAddressAndPortDependent, filtering: FilteringEndpointIndependent})
	script.local = script.mapped1
	profiler, err := New(script, fastProfilerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer profiler.Close()
	profile, err := profiler.Profile(context.Background(), script.primary)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Translation != TranslationAbsent || profile.Mapping != MappingEndpointIndependent || profile.Status != ProfileComplete {
		t.Fatalf("unexpected no-translation profile: %+v", profile)
	}
	for _, request := range script.requestSnapshot() {
		if request.Destination != script.primary {
			t.Fatalf("mapping request was not skipped: %+v", request)
		}
	}
}

func TestWildcardLocalDoesNotImplyTranslationAbsent(t *testing.T) {
	script := newIPv4Script(scriptOptions{mapping: MappingEndpointIndependent, filtering: FilteringEndpointIndependent})
	script.local = netip.MustParseAddrPort("0.0.0.0:41000")
	profiler, err := New(script, fastProfilerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer profiler.Close()
	profile, err := profiler.Profile(context.Background(), script.primary)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Translation != TranslationUnknown || profile.Mapping != MappingEndpointIndependent {
		t.Fatalf("wildcard profile = %+v", profile)
	}
}

func assertFilteringBeforeMapping(t *testing.T, script *scriptExchanger) {
	t.Helper()
	requests := script.requestSnapshot()
	seenFiltering := false
	for _, request := range requests {
		if request.ChangeIP || request.ChangePort {
			seenFiltering = true
		}
		if request.Destination != script.primary && !seenFiltering {
			t.Fatalf("alternate mapping destination preceded filtering: %+v", requests)
		}
	}
	if !seenFiltering {
		t.Fatalf("filtering request missing: %+v", requests)
	}
}

func assertRequiredLiveness(t *testing.T, profile Profile, behavior FilteringBehavior) {
	t.Helper()
	steps := make(map[TestStep]bool)
	for _, observation := range profile.Observations {
		steps[observation.Step] = true
	}
	if behavior != FilteringEndpointIndependent && !steps[StepFilteringLivenessAfterChangeIPPort] {
		t.Fatal("first filtering timeout lacked liveness check")
	}
	if behavior == FilteringAddressAndPortDependent && !steps[StepFilteringFinalLiveness] {
		t.Fatal("second filtering timeout lacked final liveness check")
	}
}

func mappingName(behavior MappingBehavior) string {
	switch behavior {
	case MappingEndpointIndependent:
		return "endpoint independent"
	case MappingAddressDependent:
		return "address dependent"
	case MappingAddressAndPortDependent:
		return "address and port dependent"
	default:
		return "unknown"
	}
}

func filteringName(behavior FilteringBehavior) string {
	switch behavior {
	case FilteringEndpointIndependent:
		return "endpoint independent"
	case FilteringAddressDependent:
		return "address dependent"
	case FilteringAddressAndPortDependent:
		return "address and port dependent"
	default:
		return "unknown"
	}
}

func TestPrimaryValidation(t *testing.T) {
	script := newIPv4Script(scriptOptions{})
	profiler, err := New(script, fastProfilerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer profiler.Close()
	for _, endpoint := range []string{"0.0.0.0:3478", "224.0.0.1:3478", "[::ffff:192.0.2.1]:3478"} {
		if _, err := profiler.Profile(context.Background(), netip.MustParseAddrPort(endpoint)); !errors.Is(err, ErrInvalidServer) {
			t.Fatalf("endpoint %s error = %v", endpoint, err)
		}
	}
	if _, err := profiler.Profile(nil, script.primary); err == nil {
		t.Fatal("nil context accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := profiler.Profile(canceled, script.primary); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled error = %v", err)
	}
}
