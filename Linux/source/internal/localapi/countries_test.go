// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	"github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

type fakeAvailableCountriesController struct {
	mu      sync.Mutex
	value   rendezvous.AvailableCountries
	refresh []bool
}

func (controller *fakeAvailableCountriesController) AvailableCountries(_ context.Context, refresh bool) (rendezvous.AvailableCountries, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.refresh = append(controller.refresh, refresh)
	return controller.value, nil
}

func TestAvailableCountriesV13MapsStatusesAndSanitizesCodes(t *testing.T) {
	statuses := []struct {
		input rendezvous.AvailableCountriesStatus
		want  localv1.AvailableCountriesStatus
	}{
		{rendezvous.AvailableCountriesReady, localv1.AvailableCountriesStatus_AVAILABLE_COUNTRIES_STATUS_READY},
		{rendezvous.AvailableCountriesPartial, localv1.AvailableCountriesStatus_AVAILABLE_COUNTRIES_STATUS_PARTIAL},
		{rendezvous.AvailableCountriesEmpty, localv1.AvailableCountriesStatus_AVAILABLE_COUNTRIES_STATUS_EMPTY},
		{rendezvous.AvailableCountriesUnavailable, localv1.AvailableCountriesStatus_AVAILABLE_COUNTRIES_STATUS_UNAVAILABLE},
	}
	for _, test := range statuses {
		t.Run(test.want.String(), func(t *testing.T) {
			controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
			countries := &fakeAvailableCountriesController{value: rendezvous.AvailableCountries{
				Status: test.input, Codes: []string{"nl", "DE", "DE", "invalid", "FR"},
				GeneratedAt: time.UnixMilli(1234),
			}}
			_, path := startTestServer(t, controller, func(config *ServerConfig) { config.Countries = countries })
			client := dialTestClient(t, path)
			result, err := client.GetAvailableCountries(context.Background(), true)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want || !slices.Equal(result.CountryCodes, []string{"DE", "FR", "NL"}) || result.GeneratedAtUnixMs != 1234 {
				t.Fatalf("result=%+v", result)
			}
			if len(countries.refresh) != 1 || !countries.refresh[0] {
				t.Fatalf("refresh=%v", countries.refresh)
			}
		})
	}
}

func TestAvailableCountriesMixedMinorCompatibility(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	countries := &fakeAvailableCountriesController{value: rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesReady, Codes: []string{"NL"}}}
	_, path := startTestServer(t, controller, func(config *ServerConfig) { config.Countries = countries })

	oldClient, err := DialVersion(context.Background(), path, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer oldClient.Close()
	if _, err := oldClient.GetStatus(context.Background()); err != nil {
		t.Fatalf("v1.2 baseline method: %v", err)
	}
	_, err = oldClient.GetAvailableCountries(context.Background(), false)
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != localv1.ResultCode_RESULT_CODE_UNSUPPORTED {
		t.Fatalf("v1.3 method under v1.2 negotiation: %v", err)
	}

	newClient := dialTestClient(t, path)
	if !newClient.SupportsV13() {
		t.Fatal("current client did not negotiate v1.3")
	}
	result, err := newClient.GetAvailableCountries(context.Background(), false)
	if err != nil || !slices.Equal(result.CountryCodes, []string{"NL"}) {
		t.Fatalf("current result=%v err=%v", result, err)
	}
}
