// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/runtime"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

type fakeController struct {
	mu              sync.Mutex
	status          runtime.Status
	connectCalls    int
	disconnectCalls int
	connectErr      error
	disconnectErr   error
	disconnectBlock <-chan struct{}
}

type fakeBootstrapController struct {
	state       *localv1.BootstrapState
	generated   *localv1.GeneratedBootstrapBundle
	imported    string
	preferred   int
	importError error
	shareError  error
}

type fakeProfileController struct {
	mu        sync.Mutex
	prepared  int
	committed int
	cancelled int
	prepareCh chan struct{}
	commitCh  chan struct{}
}

type fakeShutdownController struct {
	prepared  chan struct{}
	committed chan struct{}
}

func (controller *fakeShutdownController) PrepareShutdown(context.Context) error {
	close(controller.prepared)
	return nil
}

func (controller *fakeShutdownController) CommitShutdown() {
	close(controller.committed)
}

func TestShutdownCommitsOnlyAfterResponseIsWritten(t *testing.T) {
	shutdown := &fakeShutdownController{prepared: make(chan struct{}), committed: make(chan struct{})}
	writeGate := make(chan struct{})
	_, path := startTestServer(t, &fakeController{status: runtime.Status{State: runtime.StateConnected}}, func(config *ServerConfig) {
		config.Shutdown = shutdown
		config.WriteTimeout = time.Second
		config.DisconnectTimeout = time.Second
		config.BeforeWriteFrame = func(envelope *localv1.Envelope) {
			if envelope.GetResponse().GetAccepted() {
				<-writeGate
			}
		}
	})
	client := dialTestClient(t, path)
	result := make(chan error, 1)
	go func() { result <- client.Shutdown(context.Background()) }()
	select {
	case <-shutdown.prepared:
	case <-time.After(time.Second):
		t.Fatal("shutdown teardown was not prepared")
	}
	select {
	case <-shutdown.committed:
		t.Fatal("daemon exit committed before API acknowledgement")
	default:
	}
	close(writeGate)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdown.committed:
	case <-time.After(time.Second):
		t.Fatal("daemon exit not committed after API acknowledgement")
	}
}

func (controller *fakeProfileController) PrepareDelete(context.Context) error {
	controller.mu.Lock()
	controller.prepared++
	prepareCh := controller.prepareCh
	controller.mu.Unlock()
	if prepareCh != nil {
		select {
		case prepareCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (controller *fakeProfileController) CommitDelete() {
	controller.mu.Lock()
	controller.committed++
	commitCh := controller.commitCh
	controller.mu.Unlock()
	if commitCh != nil {
		select {
		case commitCh <- struct{}{}:
		default:
		}
	}
}

func (controller *fakeProfileController) CancelDelete() {
	controller.mu.Lock()
	controller.cancelled++
	controller.mu.Unlock()
}

func (controller *fakeProfileController) counts() (int, int, int) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.prepared, controller.committed, controller.cancelled
}

func (controller *fakeBootstrapController) State() *localv1.BootstrapState {
	return controller.state
}

func (controller *fakeBootstrapController) Import(code string) (*localv1.BootstrapState, error) {
	controller.imported = code
	return controller.state, controller.importError
}

func (controller *fakeBootstrapController) Generate(_ context.Context, preferred int) (*localv1.GeneratedBootstrapBundle, error) {
	controller.preferred = preferred
	return controller.generated, controller.shareError
}

func (controller *fakeController) Status() runtime.Status {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.status
}

func (controller *fakeController) Connect(_ context.Context, country exitselector.CountryIntent) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.connectCalls++
	if controller.connectErr == nil {
		controller.status.Country = country
		controller.status.State = runtime.StateProtecting
	}
	return controller.connectErr
}

func (controller *fakeController) Disconnect(ctx context.Context) error {
	if controller.disconnectBlock != nil {
		select {
		case <-controller.disconnectBlock:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.disconnectCalls++
	if controller.disconnectErr == nil {
		controller.status.State = runtime.StateDisconnecting
	}
	return controller.disconnectErr
}

func TestDisconnectUsesItsFullBoundedTeardownBudget(t *testing.T) {
	block := make(chan struct{})
	controller := &fakeController{
		status:          runtime.Status{State: runtime.StateConnected, Country: "DE"},
		disconnectBlock: block,
	}
	_, path := startTestServer(t, controller, func(config *ServerConfig) {
		config.RequestTimeout = 10 * time.Millisecond
		config.DisconnectTimeout = 100 * time.Millisecond
	})
	client := dialTestClient(t, path)
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(block)
	}()
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatalf("bounded disconnect rejected before teardown budget: %v", err)
	}
	_, disconnects := controller.counts()
	if disconnects != 1 {
		t.Fatalf("Disconnect calls=%d", disconnects)
	}
}
func (controller *fakeController) setStatus(status runtime.Status) {
	controller.mu.Lock()
	controller.status = status
	controller.mu.Unlock()
}

func (controller *fakeController) counts() (int, int) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.connectCalls, controller.disconnectCalls
}

func dialTestClient(t *testing.T, path string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestAPIVersionNegotiation(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	_, path := startTestServer(t, controller, nil)
	for _, minor := range []uint32{0, 7} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		client, err := DialVersion(ctx, path, 1, minor)
		cancel()
		if err != nil {
			t.Fatalf("1.%d: %v", minor, err)
		}
		want := minor
		if want > APIMinor {
			want = APIMinor
		}
		if got := client.Hello().NegotiatedApiMinor; got != want {
			t.Fatalf("1.%d negotiated minor %d", minor, got)
		}
		_ = client.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := DialVersion(ctx, path, 2, 0)
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != localv1.ResultCode_RESULT_CODE_UNSUPPORTED_API {
		t.Fatalf("major mismatch: %v", err)
	}
}

func TestV10ClientRetainsBaselineMethodsAndRejectsV11Locally(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	_, path := startTestServer(t, controller, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	client, err := DialVersion(ctx, path, 1, 0)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.GetStatus(context.Background()); err != nil {
		t.Fatalf("v1.0 GET_STATUS: %v", err)
	}
	_, err = client.GetRecentLogs(context.Background(), 10)
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != localv1.ResultCode_RESULT_CODE_UNSUPPORTED {
		t.Fatalf("v1.1 method on negotiated v1.0: %v", err)
	}
	_, err = client.call(context.Background(), &localv1.Request{Method: localv1.Method_METHOD_GET_BOOTSTRAP_STATE})
	if !errors.As(err, &responseErr) || responseErr.Code != localv1.ResultCode_RESULT_CODE_UNSUPPORTED {
		t.Fatalf("daemon accepted v1.1 method after v1.0 negotiation: %v", err)
	}
}

func TestBootstrapAPIHandlersUseProductController(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	bootstrap := &fakeBootstrapController{
		state: &localv1.BootstrapState{
			Phase: localv1.BootstrapPhase_BOOTSTRAP_PHASE_EMPTY,
		},
		generated: &localv1.GeneratedBootstrapBundle{
			Code: "irvio-bootstrap:v1:test", NodeCount: 3, GeneratedAtUnixMs: 42,
		},
	}
	_, path := startTestServer(t, controller, func(config *ServerConfig) {
		config.Bootstrap = bootstrap
	})
	client := dialTestClient(t, path)
	state, err := client.GetBootstrapState(context.Background())
	if err != nil || state.Phase != localv1.BootstrapPhase_BOOTSTRAP_PHASE_EMPTY {
		t.Fatalf("bootstrap state=%v err=%v", state, err)
	}
	state, err = client.ImportBootstrapBundle(context.Background(), "irvio-bootstrap:v1:test")
	if err != nil || state == nil || bootstrap.imported != "irvio-bootstrap:v1:test" {
		t.Fatalf("bootstrap import state=%v imported=%q err=%v", state, bootstrap.imported, err)
	}
	generated, err := client.GenerateBootstrapBundle(context.Background(), 3)
	if err != nil || generated.Code != bootstrap.generated.Code || bootstrap.preferred != 3 {
		t.Fatalf("generated=%v preferred=%d err=%v", generated, bootstrap.preferred, err)
	}
}

func TestDeleteProfileRequiresV12ConfirmationAndDeliveredResponse(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	profile := &fakeProfileController{
		prepareCh: make(chan struct{}, 1),
		commitCh:  make(chan struct{}, 1),
	}
	writeGate := make(chan struct{})
	_, path := startTestServer(t, controller, func(config *ServerConfig) {
		config.Profile = profile
		config.WriteTimeout = time.Second
		config.BeforeWriteFrame = func(envelope *localv1.Envelope) {
			if envelope.GetResponse().GetAccepted() {
				<-writeGate
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	v11, err := DialVersion(ctx, path, 1, 1)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if err := v11.DeleteProfile(context.Background()); err == nil {
		t.Fatal("v1.1 client accepted v1.2 profile deletion")
	}
	_, err = v11.call(context.Background(), &localv1.Request{
		Method:        localv1.Method_METHOD_DELETE_PROFILE,
		DeleteProfile: &localv1.DeleteProfileRequest{Confirm: true},
	})
	var compatibilityErr *ResponseError
	if !errors.As(err, &compatibilityErr) || compatibilityErr.Code != localv1.ResultCode_RESULT_CODE_UNSUPPORTED {
		t.Fatalf("raw v1.1 profile deletion error = %v", err)
	}
	if prepared, committed, cancelled := profile.counts(); prepared != 0 || committed != 0 || cancelled != 0 {
		t.Fatalf("v1.1 request reached profile controller: prepared=%d committed=%d cancelled=%d", prepared, committed, cancelled)
	}
	_ = v11.Close()

	client := dialTestClient(t, path)
	_, err = client.call(context.Background(), &localv1.Request{
		Method:        localv1.Method_METHOD_DELETE_PROFILE,
		DeleteProfile: &localv1.DeleteProfileRequest{},
	})
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != localv1.ResultCode_RESULT_CODE_INVALID_ARGUMENT {
		t.Fatalf("unconfirmed deletion error = %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- client.DeleteProfile(context.Background()) }()
	select {
	case <-profile.prepareCh:
	case <-time.After(time.Second):
		t.Fatal("profile deletion was not prepared")
	}
	if prepared, committed, cancelled := profile.counts(); prepared != 1 || committed != 0 || cancelled != 0 {
		t.Fatalf("before response write: prepared=%d committed=%d cancelled=%d", prepared, committed, cancelled)
	}
	close(writeGate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-profile.commitCh:
	case <-time.After(time.Second):
		t.Fatal("profile deletion was not committed after response delivery")
	}
}

func TestConnectionUptimeFollowsLogicalLifecycle(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	server, path := startTestServer(t, controller, func(config *ServerConfig) {
		config.Now = func() time.Time { return now }
		config.PollInterval = time.Hour
	})
	client := dialTestClient(t, path)
	if err := client.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	controller.setStatus(runtime.Status{State: runtime.StateConnected, Country: "DE"})
	server.ObserveRuntimeStatus(controller.Status())
	status, err := client.GetStatus(context.Background())
	if err != nil || status.GetConnection().GetConnectedSinceUnixMs() != uint64(now.UnixMilli()) {
		t.Fatalf("connected uptime=%v err=%v", status, err)
	}
	now = now.Add(15 * time.Minute)
	controller.setStatus(runtime.Status{State: runtime.StateSamePeerRecovery, Country: "DE"})
	server.ObserveRuntimeStatus(controller.Status())
	status, err = client.GetStatus(context.Background())
	if err != nil || status.GetConnection().GetConnectedSinceUnixMs() != uint64(now.Add(-15*time.Minute).UnixMilli()) {
		t.Fatalf("recovery reset uptime=%v err=%v", status, err)
	}
	restartedGUI := dialTestClient(t, path)
	status, err = restartedGUI.GetStatus(context.Background())
	if err != nil || status.GetConnection().GetConnectedSinceUnixMs() != uint64(now.Add(-15*time.Minute).UnixMilli()) {
		t.Fatalf("GUI restart reset uptime=%v err=%v", status, err)
	}
	controller.setStatus(runtime.Status{State: runtime.StateIdle})
	server.ObserveRuntimeStatus(controller.Status())
	status, err = client.GetStatus(context.Background())
	if err != nil || status.GetConnection() != nil {
		t.Fatalf("disconnect did not clear uptime=%v err=%v", status, err)
	}
	now = now.Add(10 * time.Minute)
	if err := client.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	controller.setStatus(runtime.Status{State: runtime.StateConnected, Country: "DE"})
	server.ObserveRuntimeStatus(controller.Status())
	status, err = client.GetStatus(context.Background())
	if err != nil || status.GetConnection().GetConnectedSinceUnixMs() != uint64(now.UnixMilli()) {
		t.Fatalf("new lifecycle uptime=%v err=%v", status, err)
	}
	now = now.Add(time.Minute)
	if err := client.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	status, err = client.GetStatus(context.Background())
	if err != nil || status.GetConnection().GetConnectedSinceUnixMs() != uint64(now.Add(-time.Minute).UnixMilli()) {
		t.Fatalf("idempotent Connect reset uptime=%v err=%v", status, err)
	}
}

func TestMultipleClientsAndBoundedAdmission(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	_, path := startTestServer(t, controller, func(config *ServerConfig) {
		config.MaxClients = 2
	})
	first := dialTestClient(t, path)
	second := dialTestClient(t, path)
	if _, err := first.GetStatus(context.Background()); err != nil {
		t.Fatalf("first client: %v", err)
	}
	if _, err := second.GetStatus(context.Background()); err != nil {
		t.Fatalf("second client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if third, err := Dial(ctx, path); err == nil {
		_ = third.Close()
		t.Fatal("client limit was not enforced")
	}
}

func TestGetStatusAndCommandIdempotence(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	_, path := startTestServer(t, controller, nil)
	client := dialTestClient(t, path)
	status, err := client.GetStatus(context.Background())
	if err != nil || status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("initial status=%v err=%v", status, err)
	}
	if err := client.Connect(context.Background(), "de"); err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(context.Background(), "DE"); err != nil {
		t.Fatal(err)
	}
	connects, _ := controller.counts()
	if connects != 1 {
		t.Fatalf("Connect(DE) invoked Core %d times", connects)
	}
	controller.setStatus(runtime.Status{State: runtime.StateIdle})
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, disconnects := controller.counts()
	if disconnects != 0 {
		t.Fatalf("idle Disconnect invoked Core %d times", disconnects)
	}
	controller.setStatus(runtime.Status{State: runtime.StateConnected, Country: "DE"})
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, disconnects = controller.counts()
	if disconnects != 1 {
		t.Fatalf("repeated Disconnect invoked Core %d times", disconnects)
	}
	if err := client.Connect(context.Background(), "Germany"); err == nil {
		t.Fatal("invalid country was accepted")
	}
}

func TestContributionBoundaryIsExplicitlyUnsupported(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	_, path := startTestServer(t, controller, nil)
	client := dialTestClient(t, path)
	_, err := client.GetContribution(context.Background())
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != localv1.ResultCode_RESULT_CODE_UNSUPPORTED {
		t.Fatalf("GetContribution: %v", err)
	}
	if err := client.SetContribution(context.Background(), true); !errors.As(err, &responseErr) || responseErr.Code != localv1.ResultCode_RESULT_CODE_UNSUPPORTED {
		t.Fatalf("SetContribution: %v", err)
	}
}

func TestEventsAndGUIReconnectDoNotOwnCoreLifecycle(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	_, path := startTestServer(t, controller, nil)
	client := dialTestClient(t, path)
	events, err := client.SubscribeEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	initial := awaitStatusEvent(t, events)
	if initial.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		t.Fatalf("initial event: %v", initial)
	}
	controller.setStatus(runtime.Status{State: runtime.StateConnected, Country: "DE", SessionGeneration: 11})
	connected := awaitStatusEvent(t, events)
	if connected.State != localv1.ConnectionState_CONNECTION_STATE_CONNECTED {
		t.Fatalf("connected event: %v", connected)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	connects, disconnects := controller.counts()
	if connects != 0 || disconnects != 0 {
		t.Fatalf("closing GUI changed Core: connect=%d disconnect=%d", connects, disconnects)
	}
	restarted := dialTestClient(t, path)
	snapshot, err := restarted.GetStatus(context.Background())
	if err != nil || snapshot.State != localv1.ConnectionState_CONNECTION_STATE_CONNECTED || snapshot.CountryCode != "DE" {
		t.Fatalf("restarted GUI status=%v err=%v", snapshot, err)
	}
	if got := controller.Status().SessionGeneration; got != 11 {
		t.Fatalf("GUI reconnect changed session generation: %d", got)
	}
}

func TestExactRuntimeEventPreservesTransientProductState(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateConnected, Country: "DE"}}
	server, path := startTestServer(t, controller, func(config *ServerConfig) {
		config.PollInterval = time.Hour
	})
	client := dialTestClient(t, path)
	events, err := client.SubscribeEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status := awaitStatusEvent(t, events); status.State != localv1.ConnectionState_CONNECTION_STATE_CONNECTED {
		t.Fatalf("initial event: %v", status)
	}
	server.ObserveRuntimeStatus(runtime.Status{State: runtime.StateSamePeerRecovery, Country: "DE"})
	if status := awaitStatusEvent(t, events); status.State != localv1.ConnectionState_CONNECTION_STATE_RECOVERING {
		t.Fatalf("transient event: %v", status)
	}
}

func TestSlowSubscriberIsDisconnectedWithoutBlockingController(t *testing.T) {
	controller := &fakeController{status: runtime.Status{State: runtime.StateIdle}}
	blockEvents := make(chan struct{})
	server, path := startTestServer(t, controller, func(config *ServerConfig) {
		config.EventQueue = 2
		config.BeforeWriteFrame = func(envelope *localv1.Envelope) {
			if envelope.GetEvent() != nil {
				<-blockEvents
			}
		}
	})
	defer close(blockEvents)
	client := dialTestClient(t, path)
	if _, err := client.SubscribeEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	var subscriber *serverSession
	for candidate := range server.subscribers {
		subscriber = candidate
	}
	startSequence := server.sequence
	server.mu.Unlock()
	if subscriber == nil {
		t.Fatal("subscriber was not registered")
	}
	states := []runtime.State{runtime.StateProtecting, runtime.StateConnected, runtime.StateSamePeerRecovery}
	for index, state := range states {
		controller.setStatus(runtime.Status{State: state, Country: "DE"})
		awaitSequence(t, server, startSequence+uint64(index)+1)
	}
	select {
	case <-subscriber.done:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber was not disconnected")
	}
	if status := controller.Status(); status.State != runtime.StateSamePeerRecovery {
		t.Fatalf("Core update was blocked: %v", status.State)
	}
}

func awaitStatusEvent(t *testing.T, events <-chan *localv1.Event) *localv1.Status {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok || event.GetStatusChanged().GetStatus() == nil {
			t.Fatalf("invalid status event: %v", event)
		}
		return event.GetStatusChanged().GetStatus()
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for status event")
		return nil
	}
}

func awaitSequence(t *testing.T, server *Server, want uint64) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for event sequence %d", want)
		case <-ticker.C:
			server.mu.Lock()
			got := server.sequence
			server.mu.Unlock()
			if got >= want {
				return
			}
		}
	}
}

func TestCredentialPolicyRejectsNilSocket(t *testing.T) {
	_, err := (CredentialPolicy{}).Authorize(nil)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}
