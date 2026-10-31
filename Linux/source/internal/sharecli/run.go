// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package sharecli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

type API interface {
	GetStatus(context.Context) (*localv1.Status, error)
	ImportBootstrapBundle(context.Context, string) (*localv1.BootstrapState, error)
	PrivateCommand(context.Context, *localv1.PrivateCommand) (*localv1.PrivateCommandResult, error)
	Disconnect(context.Context) error
}

func Run(ctx context.Context, api API, bootstrap, directory string, out io.Writer) (result error) {
	return RunPersistent(ctx, api, bootstrap, directory, out, false)
}

func RunPersistent(ctx context.Context, api API, bootstrap, directory string, out io.Writer, persistent bool) (result error) {
	startup, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	status, err := api.GetStatus(startup)
	if err != nil {
		return err
	}
	if status.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
		return errors.New("IRVIO is already active; disconnect in the UI before starting console sharing")
	}
	file, err := createInvitationFile(directory)
	if err != nil {
		return fmt.Errorf("cannot create invitation in current directory: %w", err)
	}
	saved := false
	defer func() {
		file.Close()
		if !saved {
			os.Remove(file.Name())
		}
	}()
	fmt.Fprintln(out, "Importing network bootstrap...")
	if _, err = api.ImportBootstrapBundle(startup, bootstrap); err != nil {
		return fmt.Errorf("bootstrap import: %w", err)
	}
	fmt.Fprintln(out, "Starting Private VPN Internet sharing...")
	_, err = api.PrivateCommand(startup, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true})
	// A timed-out request may still have installed intent; cleanup uses a fresh context.
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("start sharing: %w", err)
	}
	defer func() {
		if saved {
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		state, e := api.GetStatus(cleanup)
		if e == nil && !state.PrivateHostingWanted {
			return
		}
		if e == nil {
			e = api.Disconnect(cleanup)
		}
		if e == nil {
			e = waitState(cleanup, api, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED)
		}
		if e != nil {
			result = errors.Join(result, fmt.Errorf("sharing cleanup not confirmed; use UI DISCONNECT: %w", e))
			return
		}
		fmt.Fprintln(out, "Sharing stopped. Profile and existing friend permissions preserved.")
	}()
	if err != nil {
		return fmt.Errorf("start sharing: %w", err)
	}
	if err = waitState(startup, api, localv1.ConnectionState_CONNECTION_STATE_HOSTING); err != nil {
		return err
	}
	invite, err := api.PrivateCommand(startup, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION, LifetimeSeconds: 3600, AutoApprove: true})
	if err != nil {
		return fmt.Errorf("create invitation: %w", err)
	}
	token, err := privateaccess.DecodeInvitation(invite.InvitationLink, time.Now())
	if err != nil {
		return errors.New("daemon returned an invalid invitation")
	}
	if _, err = io.WriteString(file, invite.InvitationLink+"\n"); err == nil {
		err = file.Sync()
	}
	if err == nil {
		err = file.Close()
	}
	if err != nil {
		_, _ = api.PrivateCommand(startup, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CANCEL_INVITATION, InvitationId: token.Invitation.ID})
		return fmt.Errorf("save invitation: %w", err)
	}
	if persistent {
		_, err = api.PrivateCommand(startup, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_SET_PERSISTENT_HOSTING, Hosting: true})
		if err != nil {
			return fmt.Errorf("enable persistent sharing: %w", err)
		}
	}
	saved = true
	fmt.Fprintf(out, "Sharing ready. Invitation file: %s\nOne device; redeem before %s. Keep this file private.\nSharing continues after SSH closes. Use 'irvio status' or 'irvio stop'.\n", file.Name(), time.Unix(token.Invitation.ExpiresAt, 0).Format(time.RFC3339))
	if persistent {
		fmt.Fprintln(out, "Persistent sharing enabled: resumes after reboot. 'irvio stop' disables it.")
	}
	return nil
}

func Status(ctx context.Context, api API, out io.Writer) error {
	state, err := api.GetStatus(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "State: %s\nPrivate sharing: %t\n", state.State, state.PrivateHostingWanted)
	return err
}

func Stop(ctx context.Context, api API, out io.Writer) error {
	state, err := api.GetStatus(ctx)
	if err != nil {
		return err
	}
	if !state.PrivateHostingWanted {
		if state.State != localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED {
			return errors.New("another VPN mode is active; use its Disconnect control")
		}
		fmt.Fprintln(out, "Private sharing is already stopped.")
		return nil
	}
	if err := api.Disconnect(ctx); err != nil {
		return err
	}
	if err := waitState(ctx, api, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED); err != nil {
		return err
	}
	fmt.Fprintln(out, "Sharing stopped. Profile and existing friend permissions preserved.")
	return nil
}

func waitState(ctx context.Context, api API, wanted localv1.ConnectionState) error {
	for {
		state, err := api.GetStatus(ctx)
		if err != nil {
			return err
		}
		if state.State == wanted {
			return nil
		}
		if wanted == localv1.ConnectionState_CONNECTION_STATE_HOSTING && (!state.PrivateHostingWanted || state.State == localv1.ConnectionState_CONNECTION_STATE_ERROR) {
			return errors.New("private hosting did not become ready")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
