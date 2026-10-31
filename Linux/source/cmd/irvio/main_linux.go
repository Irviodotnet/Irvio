// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Irviodotnet/Irvio/internal/localapi"
	"github.com/Irviodotnet/Irvio/internal/sharecli"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "IRVIO:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) == 1 {
		if os.Geteuid() == 0 {
			return errors.New("open the UI as your desktop user, not root")
		}
		return syscall.Exec("/opt/irvio/ui/irvio", []string{"irvio"}, os.Environ())
	}
	flags := flag.NewFlagSet("irvio", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), cliHelpIntro)
		flags.PrintDefaults()
		fmt.Fprint(flags.Output(), cliHelpExamples)
	}
	mode := flags.String("mode", "", "client: share your Internet privately, without a GUI")
	bootstrap := flags.String("bootstrap", "", "network bootstrap code, text file or PNG/JPEG QR file")
	persistent := flags.Bool("persistent", false, "resume private sharing after reboot until explicitly stopped")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	command := ""
	if flags.NArg() == 1 && *mode == "" && *bootstrap == "" && !*persistent {
		command = flags.Arg(0)
	}
	if command != "status" && command != "stop" && (*mode != "client" || *bootstrap == "" || flags.NArg() != 0) {
		return errors.New("usage: irvio --mode client --bootstrap <code|text-file|QR-image> | irvio status | irvio stop")
	}
	var code string
	var err error
	if command == "" {
		code, err = sharecli.ReadBootstrap(*bootstrap)
		if err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	dial, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := localapi.Dial(dial, localapi.DefaultSocketPath)
	if err != nil {
		return fmt.Errorf("Local API unavailable; install IRVIO for this user and enable irvio.socket: %w", err)
	}
	defer client.Close()
	if !client.SupportsPrivateNetwork() {
		return errors.New("installed daemon does not support Private VPN; update IRVIO")
	}
	if *persistent && client.Hello().NegotiatedApiMinor < 8 {
		return errors.New("persistent sharing requires an updated daemon and boot service; reinstall the current package")
	}
	if *persistent {
		if _, err := os.Stat("/etc/systemd/system/multi-user.target.wants/irvio-persistent.service"); err != nil {
			return errors.New("persistent boot service is not installed/enabled; install the current IRVIO package")
		}
	}
	if command != "" {
		operation, done := context.WithTimeout(ctx, 60*time.Second)
		defer done()
		if command == "status" {
			if err := sharecli.Status(operation, client, os.Stdout); err != nil {
				return err
			}
			if client.Hello().NegotiatedApiMinor >= 8 {
				state, err := client.GetPrivateState(operation, 0, 1)
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stdout, "Persistent sharing: %t\n", state.PersistentHosting)
			}
			return nil
		}
		return sharecli.Stop(operation, client, os.Stdout)
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return sharecli.RunPersistent(ctx, client, code, dir, os.Stdout, *persistent)
}

const cliHelpIntro = `IRVIO for Linux

Commands:
  irvio             Open the graphical client (Global VPN / Private VPN).
  irvio status      Show connection state and persistent-sharing setting.
  irvio stop        Stop private sharing and disable its reboot recovery.
  irvio --help, -h  Show this help; no daemon connection is required.

Console startup options (irvio):
`

const cliHelpExamples = `
Examples:

  Client with UI:
    irvio

  Client without UI: share your Internet with a friend:
    cd ~
    irvio --mode client --bootstrap 'irvio-bootstrap:v1:<FULL_BUNDLE>'

  A bootstrap text file or PNG/JPEG QR image also works:
    irvio --mode client --bootstrap "$HOME/bootstrap.txt"
    irvio --mode client --bootstrap "$HOME/bootstrap.png"

  Persistent client: restore private sharing after an OS reboot:
    irvio --mode client --bootstrap "$HOME/bootstrap.txt" --persistent

  Check or stop sharing:
    irvio status
    irvio stop

Server (public distributor + Exit Node; separate irviod executable):
  Install from the extracted tarball and enable the systemd service:
    sudo ./install.sh --role server --bootstrap 'irvio-bootstrap:v1:<FULL_BUNDLE>'
    sudo /opt/irvio/scripts/check-server.sh

  Manual foreground launch instead of the service:
    sudo /opt/irvio/bin/irviod --role server --bootstrap 'irvio-bootstrap:v1:<FULL_BUNDLE>'

  Server product options: --role server, --bootstrap <network bundle>.
  For daemon/engineering options: /opt/irvio/bin/irviod --help
  Do not run a manual daemon alongside irvio-server.service.
  Do not install desktop and server presets over each other on one host.

Notes:
  Console client requires the installed desktop daemon and irvio.socket.
  Client installation from the extracted tarball: sudo ./install.sh --user "$USER"
  Run client commands as your normal user, from a directory you can access.
  --mode client means Private VPN Internet sharing, not a Global VPN connection.
  --bootstrap takes a NETWORK bootstrap, not a friend's private invitation.
  The invitation is saved as irvio-invitation-*.txt in the current directory.
  After "Sharing ready", you may close SSH, with or without --persistent.
  Only --persistent restores sharing after reboot; irvio stop disables it.
  First-use invitations expire after one hour; accepted access lasts until revoked.

Logs:
  Client: sudo journalctl -u irviod.service -f
  Server: sudo journalctl -u irvio-server.service -f
`
