// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// hydra-test-product-api drives focused product acceptance through the real Local API.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: hydra-test-product-api bundle <multiaddrs...> | status|bootstrap|countries|connect|disconnect|shutdown|delete|import <socket> [value]")
	}
	if os.Args[1] == "bundle" {
		bundle, err := productbootstrap.FromMultiaddrs(os.Args[2:], 0)
		if err != nil {
			return err
		}
		code, err := productbootstrap.Encode(bundle)
		if err != nil {
			return err
		}
		fmt.Println(code)
		return nil
	}
	if os.Args[1] == "bundle-addresses" || os.Args[1] == "bundle-from-store" {
		if len(os.Args) != 3 {
			return fmt.Errorf("bootstrap file required")
		}
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			return err
		}
		var bundle *localv1.BootstrapBundle
		if os.Args[1] == "bundle-from-store" {
			bundle, err = productbootstrap.DecodeStored(data)
		} else {
			bundle, err = productbootstrap.Decode(string(data))
		}
		if err != nil {
			return err
		}
		if os.Args[1] == "bundle-from-store" {
			code, err := productbootstrap.Encode(bundle)
			if err != nil {
				return err
			}
			fmt.Println(code)
			return nil
		}
		addresses, err := productbootstrap.Multiaddrs(bundle)
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(addresses, "\n"))
		return nil
	}
	if len(os.Args) < 3 {
		return fmt.Errorf("socket path required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	client, err := localapi.Dial(ctx, os.Args[2])
	if err != nil {
		return err
	}
	defer client.Close()
	switch os.Args[1] {
	case "private-state":
		state, err := client.GetPrivateState(ctx, 0, 64)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(state)
	case "traffic":
		state, err := client.GetTrafficStats(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(state)
	case "logs":
		logs, err := client.GetRecentLogs(ctx, localapi.MaxLogTailRecords)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(logs)
	case "import-file", "private-import-file":
		if len(os.Args) != 4 {
			return fmt.Errorf("input file required")
		}
		data, err := os.ReadFile(os.Args[3])
		if err != nil {
			return err
		}
		if os.Args[1] == "import-file" {
			_, err = client.ImportBootstrapBundle(ctx, strings.TrimSpace(string(data)))
			return err
		}
		result, err := client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_IMPORT_INVITATION, InvitationLink: strings.TrimSpace(string(data)), Name: "mixed-platform-lab"})
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"target_peer_id": result.TargetPeerId})
	case "private-host":
		_, err := client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CONNECT, Hosting: true})
		return err
	case "private-invite-file":
		if len(os.Args) != 4 {
			return fmt.Errorf("new invitation output file required")
		}
		file, err := os.OpenFile(os.Args[3], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		result, err := client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: localv1.PrivateAction_PRIVATE_ACTION_CREATE_INVITATION, LifetimeSeconds: 3600, AutoApprove: true})
		if err != nil {
			return err
		}
		if _, err := file.WriteString(result.InvitationLink + "\n"); err != nil {
			return err
		}
		return file.Sync()
	case "private-enroll", "private-connect", "private-connect-host", "private-revoke", "private-remove-target":
		if len(os.Args) != 4 {
			return fmt.Errorf("peer ID required")
		}
		action := localv1.PrivateAction_PRIVATE_ACTION_CONNECT
		if os.Args[1] == "private-enroll" {
			action = localv1.PrivateAction_PRIVATE_ACTION_RESUME_ENROLLMENT
		} else if os.Args[1] == "private-revoke" {
			action = localv1.PrivateAction_PRIVATE_ACTION_REVOKE_MEMBER
		} else if os.Args[1] == "private-remove-target" {
			action = localv1.PrivateAction_PRIVATE_ACTION_REMOVE_TARGET
		}
		result, err := client.PrivateCommand(ctx, &localv1.PrivateCommand{Action: action, PeerId: os.Args[3], Hosting: os.Args[1] == "private-connect-host"})
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "private-summary":
		state, err := client.GetPrivateState(ctx, 0, 64)
		if err != nil {
			return err
		}
		status, err := client.GetStatus(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"state": status.GetState().String(), "saved_targets": len(state.GetTargets()),
			"private_target_active": status.GetPrivateTargetPeerId() != "", "hosting_wanted": status.GetPrivateHostingWanted(),
			"contribution": status.GetContributionEnabled(),
		})
	case "bootstrap":
		state, err := client.GetBootstrapState(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"configured": state.GetConfiguredCount(), "reachable": state.GetCurrentlyReachableCount(),
		})
	case "status":
		status, err := client.GetStatus(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"state": status.GetState().String(), "country": status.GetCountryCode(),
			"contribution": status.GetContributionEnabled(), "connection": status.GetConnection(),
			"error": status.GetError(),
		})
	case "countries":
		available, err := client.GetAvailableCountries(ctx, true)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"status": available.GetStatus().String(), "countries": available.GetCountryCodes(),
			"generated_at_unix_ms": available.GetGeneratedAtUnixMs(),
		})
	case "connect":
		if len(os.Args) != 4 {
			return fmt.Errorf("country required")
		}
		return client.Connect(ctx, os.Args[3])
	case "disconnect":
		return client.Disconnect(ctx)
	case "shutdown":
		return client.Shutdown(ctx)
	case "delete":
		return client.DeleteProfile(ctx)
	case "import":
		if len(os.Args) != 4 {
			return fmt.Errorf("bundle code required")
		}
		_, err := client.ImportBootstrapBundle(ctx, os.Args[3])
		return err
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
