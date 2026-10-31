// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	"github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

type privateDependentCountries struct {
	started        chan struct{}
	privateStopped <-chan struct{}
}

func (reader privateDependentCountries) AvailableCountries(ctx context.Context, _ bool) (rendezvous.AvailableCountries, error) {
	close(reader.started)
	<-ctx.Done()
	<-reader.privateStopped
	return rendezvous.AvailableCountries{}, ctx.Err()
}

type blockingPrivateController struct {
	fakePrivateController
	started chan struct{}
	stopped chan struct{}
}

func (controller *blockingPrivateController) PrivateCommand(ctx context.Context, _ *localv1.PrivateCommand) (*localv1.PrivateCommandResult, error) {
	close(controller.started)
	<-ctx.Done()
	close(controller.stopped)
	return nil, ctx.Err()
}

func awaitPrivateSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("private operation did not progress")
	}
}

func TestPrivateOperationKeepsSessionResponsiveAndDisconnectCancelsGlobally(t *testing.T) {
	backend := &blockingPrivateController{started: make(chan struct{}), stopped: make(chan struct{})}
	core := &fakeController{status: runtime.Status{State: runtime.StateConnected}}
	_, path := startTestServer(t, core, func(config *ServerConfig) { config.Private = backend })
	client, other := dialTestClient(t, path), dialTestClient(t, path)
	command := &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true}
	finished := make(chan error, 1)
	go func() { _, err := client.PrivateCommand(context.Background(), command); finished <- err }()
	awaitPrivateSignal(t, backend.started)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.GetStatus(ctx); err != nil {
		t.Fatalf("same-session status blocked: %v", err)
	}
	if _, err := client.GetPrivateState(ctx, 0, 32); err != nil {
		t.Fatalf("same-session private read blocked: %v", err)
	}
	_, err := other.PrivateCommand(ctx, command)
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != localv1.ResultCode_RESULT_CODE_BUSY {
		t.Fatalf("second mutation not rejected: %v", err)
	}
	if err := other.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	awaitPrivateSignal(t, backend.stopped)
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled operation reported success")
		}
	case <-ctx.Done():
		t.Fatal("canceled operation response missing")
	}
	_, disconnects := core.counts()
	if disconnects != 1 {
		t.Fatal("disconnect did not reach runtime after private cancellation")
	}
}

func TestPrivateOperationGUIDeathDoesNotDisconnectExistingVPN(t *testing.T) {
	backend := &blockingPrivateController{started: make(chan struct{}), stopped: make(chan struct{})}
	core := &fakeController{status: runtime.Status{State: runtime.StateConnected}}
	_, path := startTestServer(t, core, func(config *ServerConfig) { config.Private = backend })
	client := dialTestClient(t, path)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = client.PrivateCommand(context.Background(), &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION, LifetimeSeconds: 3600})
	}()
	awaitPrivateSignal(t, backend.started)
	client.Close()
	awaitPrivateSignal(t, backend.stopped)
	awaitPrivateSignal(t, finished)
	_, disconnects := core.counts()
	if disconnects != 0 || core.Status().State != runtime.StateConnected {
		t.Fatal("GUI death disconnected active runtime")
	}
}

func TestPrivateLifecycleBarrierPreventsNewCommandsUntilReleased(t *testing.T) {
	server := &Server{}
	ctx, finish, ok := server.startPrivateOperation(context.Background())
	if !ok {
		t.Fatal("first operation rejected")
	}
	done := make(chan func(), 1)
	go func() { release, _ := server.stopPrivateOperation(context.Background()); done <- release }()
	awaitPrivateSignal(t, ctx.Done())
	if _, _, ok := server.startPrivateOperation(context.Background()); ok {
		t.Fatal("operation admitted while canceling")
	}
	finish()
	release := <-done
	if _, _, ok := server.startPrivateOperation(context.Background()); ok {
		t.Fatal("operation admitted during teardown")
	}
	release()
	_, finish, ok = server.startPrivateOperation(context.Background())
	if !ok {
		t.Fatal("completed teardown retained operation fence")
	}
	finish()
}

func TestDisconnectCancelsPrivateBeforeJoiningBlockedCountryQuery(t *testing.T) {
	backend := &blockingPrivateController{started: make(chan struct{}), stopped: make(chan struct{})}
	countries := privateDependentCountries{started: make(chan struct{}), privateStopped: backend.stopped}
	_, path := startTestServer(t, &fakeController{status: runtime.Status{State: runtime.StateConnected}}, func(config *ServerConfig) {
		config.Private, config.Countries = backend, countries
	})
	client := dialTestClient(t, path)
	privateDone, countryDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(privateDone)
		_, _ = client.PrivateCommand(context.Background(), &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true})
	}()
	awaitPrivateSignal(t, backend.started)
	go func() { defer close(countryDone); _, _ = client.GetAvailableCountries(context.Background(), true) }()
	awaitPrivateSignal(t, countries.started)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Disconnect(ctx); err != nil {
		t.Fatalf("cancellation order blocked Disconnect: %v", err)
	}
	awaitPrivateSignal(t, privateDone)
	awaitPrivateSignal(t, countryDone)
}
