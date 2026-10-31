// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func privateLifecycleBarrier(method localv1.Method) bool {
	switch method {
	case localv1.Method_METHOD_CONNECT, localv1.Method_METHOD_DISCONNECT, localv1.Method_METHOD_SHUTDOWN,
		localv1.Method_METHOD_DELETE_PROFILE, localv1.Method_METHOD_IMPORT_BOOTSTRAP_BUNDLE:
		return true
	}
	return false
}

// startPrivateOperation admits one private mutation globally, without a waiting queue.
func (server *Server) startPrivateOperation(parent context.Context) (context.Context, func(), bool) {
	server.privateOpMu.Lock()
	defer server.privateOpMu.Unlock()
	if server.privateDone != nil || server.privateStopping != 0 || parent.Err() != nil {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	server.privateCancel, server.privateDone = cancel, done
	return ctx, func() {
		cancel()
		server.privateOpMu.Lock()
		defer server.privateOpMu.Unlock()
		server.privateCancel, server.privateDone = nil, nil
		close(done)
	}, true
}

// stopPrivateOperation keeps new private work fenced until the caller finishes its lifecycle action.
func (server *Server) stopPrivateOperation(ctx context.Context) (func(), error) {
	server.privateOpMu.Lock()
	server.privateStopping++
	cancel, done := server.privateCancel, server.privateDone
	server.privateOpMu.Unlock()
	release := func() {
		server.privateOpMu.Lock()
		server.privateStopping--
		server.privateOpMu.Unlock()
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	return release, nil
}
