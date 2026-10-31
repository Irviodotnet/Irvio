// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"context"
	"crypto/sha256"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type testAuthority struct {
	mu             sync.Mutex
	lease          tunnellease.Lease
	renewCalls     int
	released       bool
	releaseStarted chan struct{}
	releaseBlock   chan struct{}
	releaseDone    chan struct{}
	rotation       RotationProposal
	rotationDigest [sha256.Size]byte
	rotationCalls  int
}

func (a *testAuthority) RotationProposal(client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken) (RotationProposal, error) {
	if _, err := a.Lookup(client, id, revision, token); err != nil {
		return RotationProposal{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rotation.KeyEpoch == 0 {
		return RotationProposal{}, ErrNoRotation
	}
	return RotationProposal{SignedBinding: append([]byte(nil), a.rotation.SignedBinding...), KeyEpoch: a.rotation.KeyEpoch}, nil
}

func (a *testAuthority) ValidateAWGRotation(client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken, epoch uint64, digest [32]byte) error {
	if _, err := a.Lookup(client, id, revision, token); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if epoch != a.rotation.KeyEpoch || digest != a.rotationDigest {
		return ErrStale
	}
	return nil
}

func (a *testAuthority) CommitAWGRotation(_ context.Context, client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken, epoch uint64, digest [32]byte) (tunnellease.Lease, error) {
	if _, err := a.Lookup(client, id, revision, token); err != nil {
		return tunnellease.Lease{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if epoch != a.rotation.KeyEpoch || digest != a.rotationDigest {
		return tunnellease.Lease{}, ErrStale
	}
	a.rotationCalls++
	a.rotation = RotationProposal{}
	return a.lease, nil
}

func (a *testAuthority) Lookup(client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken) (tunnellease.Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.released {
		return tunnellease.Lease{}, tunnellease.ErrUnknownLease
	}
	if client != a.lease.ClientPeer {
		return tunnellease.Lease{}, tunnellease.ErrWrongPeer
	}
	if id != a.lease.ID || revision != a.lease.Generation || token != a.lease.Session {
		return tunnellease.Lease{}, tunnellease.ErrStaleGeneration
	}
	return a.lease, nil
}

func (a *testAuthority) Renew(client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken) (tunnellease.Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.released {
		return tunnellease.Lease{}, tunnellease.ErrUnknownLease
	}
	if client != a.lease.ClientPeer {
		return tunnellease.Lease{}, tunnellease.ErrWrongPeer
	}
	if id != a.lease.ID || revision != a.lease.Generation || token != a.lease.Session {
		return tunnellease.Lease{}, tunnellease.ErrStaleGeneration
	}
	a.renewCalls++
	a.lease.Generation++
	a.lease.ValidUntil = time.Now().Add(time.Minute)
	return a.lease, nil
}

func (a *testAuthority) Release(_ context.Context, client peer.ID, id tunnellease.ID, revision uint64, token tunnellease.SessionToken) error {
	if _, err := a.Lookup(client, id, revision, token); err != nil {
		return err
	}
	if a.releaseStarted != nil {
		close(a.releaseStarted)
	}
	if a.releaseBlock != nil {
		<-a.releaseBlock
	}
	a.mu.Lock()
	if !a.released {
		a.released = true
		if a.releaseDone != nil {
			close(a.releaseDone)
		}
	}
	a.mu.Unlock()
	return nil
}

func sessionIdentity(t *testing.T) (crypto.PrivKey, peer.ID) {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, id
}

func testLease(exit, client peer.ID) tunnellease.Lease {
	return tunnellease.Lease{
		ID: tunnellease.ID{1}, ExitPeer: exit, ClientPeer: client,
		IPv4: netip.MustParsePrefix("10.88.0.2/32"), Generation: 1,
		Session: tunnellease.SessionToken{2}, ValidUntil: time.Now().Add(time.Minute),
	}
}

func TestNoiseSessionControlBindsRenewsRebindsAndReleases(t *testing.T) {
	exitKey, exitPeer := sessionIdentity(t)
	clientKey, clientPeer := sessionIdentity(t)
	authority := &testAuthority{
		lease: testLease(exitPeer, clientPeer), releaseStarted: make(chan struct{}),
		releaseBlock: make(chan struct{}), releaseDone: make(chan struct{}),
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener, exitKey, authority, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	dial := (&net.Dialer{}).DialContext
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, listener.Addr().String(), clientKey, exitPeer, authority.lease, dial, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	initial := authority.lease
	requestID, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := client.RenewWithRequestID(ctx, initial, requestID)
	if err != nil || renewed.Generation != 2 || authority.renewCalls != 1 {
		t.Fatalf("renewed=%+v calls=%d err=%v", renewed, authority.renewCalls, err)
	}
	replayed, err := client.RenewWithRequestID(ctx, initial, requestID)
	if err != nil || replayed.Generation != 2 || authority.renewCalls != 1 {
		t.Fatalf("replayed=%+v calls=%d err=%v", replayed, authority.renewCalls, err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	rebound, err := Dial(ctx, listener.Addr().String(), clientKey, exitPeer, renewed, dial, time.Second)
	if err != nil {
		t.Fatalf("direct rebind: %v", err)
	}
	state, err := rebound.State(ctx, renewed)
	if err != nil || state.Generation != renewed.Generation {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	releaseResult := make(chan error, 1)
	go func() { releaseResult <- rebound.Release(ctx, state) }()
	select {
	case err := <-releaseResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("RELEASE_ACCEPTED was blocked behind resource revocation")
	}
	select {
	case <-authority.releaseStarted:
	case <-ctx.Done():
		t.Fatal("release cleanup did not start after response delivery")
	}
	close(authority.releaseBlock)
	select {
	case <-authority.releaseDone:
	case <-ctx.Done():
		t.Fatal("release did not reach authoritative owner")
	}
}

func TestReconnectingClientRepairsBrokenNoiseChannelWithoutBroker(t *testing.T) {
	exitKey, exitPeer := sessionIdentity(t)
	clientKey, clientPeer := sessionIdentity(t)
	authority := &testAuthority{lease: testLease(exitPeer, clientPeer)}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener, exitKey, authority, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := DialReconnecting(ctx, ReconnectingConfig{
		Address: listener.Addr().String(), PrivateKey: clientKey, ExpectedExit: exitPeer,
		Lease: authority.lease, Dial: (&net.Dialer{}).DialContext,
		Timeout: time.Second, RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.mu.Lock()
	if err := client.client.Close(); err != nil {
		client.mu.Unlock()
		t.Fatal(err)
	}
	client.mu.Unlock()
	renewed, err := client.Renew(ctx, authority.lease)
	if err != nil || renewed.Generation != 2 || authority.renewCalls != 1 {
		t.Fatalf("renewed=%+v calls=%d err=%v", renewed, authority.renewCalls, err)
	}
}

func TestReconnectingClientReservesBudgetForReleaseAfterSilentBlackhole(t *testing.T) {
	exitKey, exitPeer := sessionIdentity(t)
	clientKey, clientPeer := sessionIdentity(t)
	authority := &testAuthority{lease: testLease(exitPeer, clientPeer)}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener, exitKey, authority, 600*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := DialReconnecting(ctx, ReconnectingConfig{
		Address: listener.Addr().String(), PrivateKey: clientKey, ExpectedExit: exitPeer,
		Lease: authority.lease, Dial: (&net.Dialer{}).DialContext,
		Timeout: 600 * time.Millisecond, RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	blackholed, peerEnd := net.Pipe()
	defer peerEnd.Close()
	client.mu.Lock()
	old := client.client.conn
	client.client.conn = blackholed
	client.mu.Unlock()
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := client.Release(ctx, authority.lease); err != nil {
		t.Fatalf("release did not rebind inside one operation budget: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 900*time.Millisecond {
		t.Fatalf("release multiplied operation timeouts: %v", elapsed)
	}
}

func TestReleaseResponseLossReturnsErrorForRetry(t *testing.T) {
	_, exitPeer := sessionIdentity(t)
	_, clientPeer := sessionIdentity(t)
	lease := testLease(exitPeer, clientPeer)
	clientSide, exitSide := net.Pipe()
	defer exitSide.Close()
	client := &Client{
		conn: clientSide, local: clientPeer, remote: exitPeer,
		lease: lease, timeout: time.Second,
	}
	received := make(chan struct{})
	go func() {
		defer close(received)
		_, _, _ = readFrame(exitSide)
		_ = exitSide.Close()
	}()
	id, err := NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.ReleaseWithRequestID(ctx, lease, id); err == nil {
		t.Fatal("unconfirmed RELEASE falsely reported success")
	}
	<-received
	if !client.closed {
		t.Fatal("uncertain RELEASE response left the inner control channel reusable")
	}
}

func TestReconnectingReleaseRetriesUnconfirmedWrite(t *testing.T) {
	for _, blackhole := range []bool{false, true} {
		name := "EOF"
		if blackhole {
			name = "response_timeout"
		}
		t.Run(name, func(t *testing.T) {
			exitKey, exitPeer := sessionIdentity(t)
			clientKey, clientPeer := sessionIdentity(t)
			authority := &testAuthority{lease: testLease(exitPeer, clientPeer), releaseDone: make(chan struct{})}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server, err := NewServer(listener, exitKey, authority, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			dials := 0
			client, err := DialReconnecting(ctx, ReconnectingConfig{
				Address: listener.Addr().String(), PrivateKey: clientKey, ExpectedExit: exitPeer, Lease: authority.lease,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					dials++
					return (&net.Dialer{}).DialContext(ctx, network, address)
				}, Timeout: 600 * time.Millisecond, RetryDelay: time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			clientSide, peerSide := net.Pipe()
			defer peerSide.Close()
			old := client.client.conn
			client.client.conn = clientSide
			_ = old.Close()
			request := make(chan *irviov1.SessionControlRequest, 1)
			go func() {
				defer peerSide.Close()
				frame, _, err := readFrame(peerSide)
				if err != nil {
					return
				}
				request <- frame.GetRequest()
				if blackhole {
					var terminal [1]byte
					_, _ = peerSide.Read(terminal[:])
				}
			}()
			started := time.Now()
			if err := client.Release(ctx, authority.lease); err != nil {
				t.Fatal(err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("release exceeded bounded retry budget: %v", elapsed)
			}
			select {
			case <-authority.releaseDone:
			case <-ctx.Done():
				t.Fatal("Exit retained lease despite successful Release")
			}
			if dials != 2 {
				t.Fatalf("dials=%d, want initial connection plus one reconnect", dials)
			}
			var original *irviov1.SessionControlRequest
			select {
			case original = <-request:
			case <-ctx.Done():
				t.Fatal("first RELEASE was not written")
			}
			server.mu.Lock()
			cached, ok := server.replays[clientPeer.String()+"/"+string(original.RequestId)]
			server.mu.Unlock()
			if !ok || cached.response.Operation != irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RELEASE_ACCEPTED {
				t.Fatal("retry changed request identity or was not accepted")
			}
		})
	}
}

func TestNoiseSessionControlCommitsExactAWGRotationOnce(t *testing.T) {
	exitKey, exitPeer := sessionIdentity(t)
	clientKey, clientPeer := sessionIdentity(t)
	signed := []byte("bounded-signed-awg-binding")
	authority := &testAuthority{
		lease:          testLease(exitPeer, clientPeer),
		rotation:       RotationProposal{SignedBinding: signed, KeyEpoch: 2},
		rotationDigest: sha256.Sum256(signed),
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener, exitKey, authority, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := DialReconnecting(ctx, ReconnectingConfig{
		Address: listener.Addr().String(), PrivateKey: clientKey, ExpectedExit: exitPeer,
		Lease: authority.lease, Dial: (&net.Dialer{}).DialContext,
		Timeout: time.Second, RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	renewed, err := client.Renew(ctx, authority.lease)
	if err != nil {
		t.Fatal(err)
	}
	proposal := client.PendingRotation()
	if proposal.KeyEpoch != 2 || string(proposal.SignedBinding) != string(signed) {
		t.Fatalf("rotation proposal=%+v", proposal)
	}
	stageCalls, commitCalls := 0, 0
	rotated, err := client.Rotate(ctx, renewed, proposal, func() error {
		stageCalls++
		return nil
	}, func() error {
		commitCalls++
		return nil
	})
	if err != nil || rotated.Generation != renewed.Generation {
		t.Fatalf("rotated=%+v err=%v", rotated, err)
	}
	if authority.rotationCalls != 1 || stageCalls != 1 || commitCalls != 1 || client.PendingRotation().KeyEpoch != 0 {
		t.Fatalf("rotation calls server=%d stage=%d commit=%d pending=%+v", authority.rotationCalls, stageCalls, commitCalls, client.PendingRotation())
	}
}

func TestRotationCommitMustMatchReadyTranscript(t *testing.T) {
	initial := &irviov1.SessionControlRequest{
		ProtocolVersion:       ProtocolVersion,
		Operation:             irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE,
		RequestId:             make([]byte, RequestIDSize),
		LeaseId:               make([]byte, len(tunnellease.ID{})),
		SessionToken:          make([]byte, len(tunnellease.SessionToken{})),
		ExpectedLeaseRevision: 3,
		AwgBindingDigest:      make([]byte, sha256.Size),
		AwgKeyEpoch:           7,
	}
	initial.RequestId[0], initial.LeaseId[0], initial.SessionToken[0], initial.AwgBindingDigest[0] = 1, 2, 3, 4
	commit := proto.Clone(initial).(*irviov1.SessionControlRequest)
	commit.Operation = irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE_COMMIT
	if !matchingRotationCommit(initial, commit) {
		t.Fatal("exact COMMIT did not match READY transcript")
	}
	mutations := []func(*irviov1.SessionControlRequest){
		func(value *irviov1.SessionControlRequest) {
			value.Operation = irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATE
		},
		func(value *irviov1.SessionControlRequest) { value.RequestId[0]++ },
		func(value *irviov1.SessionControlRequest) { value.LeaseId[0]++ },
		func(value *irviov1.SessionControlRequest) { value.SessionToken[0]++ },
		func(value *irviov1.SessionControlRequest) { value.ExpectedLeaseRevision++ },
		func(value *irviov1.SessionControlRequest) { value.AwgBindingDigest[0]++ },
		func(value *irviov1.SessionControlRequest) { value.AwgKeyEpoch++ },
	}
	for index, mutate := range mutations {
		altered := proto.Clone(commit).(*irviov1.SessionControlRequest)
		mutate(altered)
		if matchingRotationCommit(initial, altered) {
			t.Fatalf("altered COMMIT %d matched", index)
		}
	}
}

func TestSessionControlReplayIsIdempotentBeforeRevisionCheck(t *testing.T) {
	exitKey, exitPeer := sessionIdentity(t)
	_, clientPeer := sessionIdentity(t)
	authority := &testAuthority{lease: testLease(exitPeer, clientPeer)}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener, exitKey, authority, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	lease := authority.lease
	request := &irviov1.SessionControlRequest{
		ProtocolVersion: ProtocolVersion,
		Operation:       irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RENEW,
		RequestId:       make([]byte, RequestIDSize), LeaseId: append([]byte(nil), lease.ID[:]...),
		SessionToken: append([]byte(nil), lease.Session[:]...), ExpectedLeaseRevision: lease.Generation,
	}
	request.RequestId[0] = 1
	frame := &irviov1.SessionControlFrame{Message: &irviov1.SessionControlFrame_Request{Request: request}}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	first := server.process(clientPeer, lease, request, payload)
	second := server.process(clientPeer, lease, request, payload)
	if first.GetLease().GetGeneration() != 2 || second.GetLease().GetGeneration() != 2 || authority.renewCalls != 1 {
		t.Fatalf("generations=%d/%d calls=%d", first.GetLease().GetGeneration(), second.GetLease().GetGeneration(), authority.renewCalls)
	}
	mutated := proto.Clone(request).(*irviov1.SessionControlRequest)
	mutated.ExpectedLeaseRevision = 7
	mutatedFrame := &irviov1.SessionControlFrame{Message: &irviov1.SessionControlFrame_Request{Request: mutated}}
	mutatedPayload, _ := proto.MarshalOptions{Deterministic: true}.Marshal(mutatedFrame)
	rejected := server.process(clientPeer, lease, mutated, mutatedPayload)
	if rejected.GetError().GetCode() != irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE {
		t.Fatalf("mutated replay response=%+v", rejected)
	}
}

func TestCachedLeaseSuccessRequiresCurrentAuthority(t *testing.T) {
	_, exitPeer := sessionIdentity(t)
	_, clientPeer := sessionIdentity(t)
	for _, operation := range []irviov1.SessionControlOperation{
		irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_RENEWED,
		irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_STATE,
		irviov1.SessionControlOperation_SESSION_CONTROL_OPERATION_AWG_ROTATED,
	} {
		t.Run(operation.String(), func(t *testing.T) {
			lease := testLease(exitPeer, clientPeer)
			authority := &testAuthority{lease: lease}
			server := &Server{authority: authority}
			request := &irviov1.SessionControlRequest{
				ProtocolVersion: ProtocolVersion, RequestId: make([]byte, RequestIDSize),
				LeaseId: lease.ID[:], SessionToken: lease.Session[:], ExpectedLeaseRevision: lease.Generation,
			}
			cached := responseLease(request, operation, lease, int64(time.Hour))
			live := server.replayResponse(clientPeer, request, cached)
			if live.Operation != operation || live.AuthoritativeRemainingNanos >= cached.AuthoritativeRemainingNanos {
				t.Fatal("live replay did not preserve result with refreshed remaining lifetime")
			}
			authority.lease.Generation++
			if got := server.replayResponse(clientPeer, request, cached); got.GetError().GetCode() != irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE {
				t.Fatal("replaced generation accepted cached success")
			}
			authority.released = true
			if got := server.replayResponse(clientPeer, request, cached); got.GetError().GetCode() != irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_NOT_FOUND {
				t.Fatal("revoked lease accepted cached success")
			}
			if authority.renewCalls != 0 || authority.rotationCalls != 0 {
				t.Fatal("replay repeated resource mutation")
			}
		})
	}
}

func TestNoiseRejectsWrongExpectedExit(t *testing.T) {
	exitKey, exitPeer := sessionIdentity(t)
	_, wrongPeer := sessionIdentity(t)
	clientKey, clientPeer := sessionIdentity(t)
	authority := &testAuthority{lease: testLease(exitPeer, clientPeer)}
	listener, _ := net.Listen("tcp4", "127.0.0.1:0")
	server, err := NewServer(listener, exitKey, authority, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = Dial(ctx, listener.Addr().String(), clientKey, wrongPeer, tunnellease.Lease{
		ID: authority.lease.ID, ExitPeer: wrongPeer, ClientPeer: clientPeer,
		IPv4: authority.lease.IPv4, Generation: authority.lease.Generation,
		Session: authority.lease.Session, ValidUntil: authority.lease.ValidUntil,
	}, (&net.Dialer{}).DialContext, 500*time.Millisecond)
	if err == nil {
		t.Fatal("Noise accepted the wrong expected Exit identity")
	}
}
