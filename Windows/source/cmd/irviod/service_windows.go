// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"time"

	"golang.org/x/sys/windows/svc"
)

type productWindowsService struct {
	run    func(context.Context, func()) error
	logger *slog.Logger
}

func (s productWindowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	current := svc.Status{State: svc.StartPending, CheckPoint: 1, WaitHint: 60000}
	changes <- current
	ready, done := make(chan struct{}), make(chan error, 1)
	var once sync.Once
	go func() { done <- s.run(ctx, func() { once.Do(func() { close(ready) }) }) }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				if s.logger != nil {
					s.logger.Error("WINDOWS_SERVICE_FAILED", "reason", err)
				}
				return true, 1
			}
			return false, 0
		case <-ready:
			ready = nil
			if current.State != svc.StopPending {
				current = svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
				changes <- current
			}
		case request, ok := <-requests:
			if !ok {
				requests = nil
				cancel()
				current = svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: 60000}
				changes <- current
				continue
			}
			switch request.Cmd {
			case svc.Interrogate:
				changes <- current
			case svc.Stop, svc.Shutdown:
				if current.State != svc.StopPending {
					current = svc.Status{State: svc.StopPending, CheckPoint: 1, WaitHint: 60000}
					changes <- current
					cancel()
				}
			}
		case <-ticker.C:
			if current.State == svc.StartPending || current.State == svc.StopPending {
				current.CheckPoint++
				changes <- current
			}
		}
	}
}

func runProduct(options productOptions) error {
	run := func(ctx context.Context, ready func()) error {
		options.ready = ready
		return runWindowsProductLoop(ctx, options, runProductWithContext)
	}
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if isService {
		return svc.Run(windowsProductService, productWindowsService{run: run, logger: options.logger})
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return run(ctx, nil)
}

func runWindowsProductLoop(ctx context.Context, options productOptions, run func(context.Context, productOptions) error) error {
	for {
		err := run(ctx, options)
		if err != errProfileResetComplete {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func recoverPreviousBoot(ctx context.Context) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return recoverWindowsPreviousBoot(ctx)
	}
	return svc.Run(windowsRecoveryService, productWindowsService{run: func(ctx context.Context, ready func()) error {
		ready()
		return recoverWindowsPreviousBoot(ctx)
	}, logger: slog.Default()})
}

func restartProductProcess() error {
	return errors.New("Windows profile reset must use the product service lifecycle")
}
