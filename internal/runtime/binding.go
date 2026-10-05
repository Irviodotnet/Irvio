// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/awg"
	"hydra.local/hydra/internal/awgbinding"
	"hydra.local/hydra/internal/peerstore"
)

type BindingDirectory interface {
	SignedAnnouncement(context.Context, string) ([]byte, error)
}

// BindingResolver converts authenticated discovery keys; reauthorize immediately before promotion.
type BindingResolver struct {
	Directory BindingDirectory
	Registry  *awgbinding.Registry
	Now       func() time.Time
	Refresh   func(context.Context, peer.ID) error
}

type ResolvedBinding struct {
	Binding awgbinding.Binding
	Key     awg.Key
}

func (r BindingResolver) Resolve(ctx context.Context, remote peer.ID) (ResolvedBinding, error) {
	if ctx == nil || r.Directory == nil || r.Registry == nil || remote == "" {
		return ResolvedBinding{}, ErrInvalidConfig
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	load := func() (awgbinding.Binding, error) {
		envelope, err := r.Directory.SignedAnnouncement(ctx, remote.String())
		if err != nil {
			return awgbinding.Binding{}, fmt.Errorf("load authenticated AWG binding: %w", err)
		}
		binding, err := awgbinding.Verify(envelope, remote, now)
		if err != nil {
			return awgbinding.Binding{}, fmt.Errorf("verify authenticated AWG binding: %w", err)
		}
		return binding, nil
	}
	binding, err := load()
	if r.Refresh != nil {
		if refreshErr := r.Refresh(ctx, remote); refreshErr == nil {
			binding, err = load()
		} else if err != nil {
			return ResolvedBinding{}, errors.Join(err, fmt.Errorf("refresh authenticated AWG binding: %w", refreshErr))
		}
	}
	if err != nil {
		return ResolvedBinding{}, err
	}
	if err := r.Registry.Commit(binding, now); err != nil {
		return ResolvedBinding{}, fmt.Errorf("commit authenticated AWG binding: %w", err)
	}
	return ResolvedBinding{Binding: binding, Key: binding.PublicKey}, nil
}

// VerifyLocator does not promote bindings; READY supplies the current descriptor before allocation.
func (r BindingResolver) VerifyLocator(signed []byte, remote peer.ID) (ResolvedBinding, error) {
	if r.Registry == nil || len(signed) == 0 || remote == "" {
		return ResolvedBinding{}, ErrInvalidConfig
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	binding, err := awgbinding.Verify(signed, remote, now)
	if err != nil {
		return ResolvedBinding{}, fmt.Errorf("verify authenticated AWG binding: %w", err)
	}
	return ResolvedBinding{Binding: binding, Key: binding.PublicKey}, nil
}

// ResolveSigned commits an authenticated M12 announcement without requiring a WAN control dial.
func (r BindingResolver) ResolveSigned(signed []byte, remote peer.ID) (ResolvedBinding, error) {
	resolved, err := r.VerifyLocator(signed, remote)
	if err != nil {
		return ResolvedBinding{}, err
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	if err := r.Registry.Commit(resolved.Binding, now); err != nil {
		return ResolvedBinding{}, fmt.Errorf("commit authenticated AWG binding: %w", err)
	}
	return resolved, nil
}

func (r BindingResolver) AuthorizePromotion(binding ResolvedBinding) error {
	if r.Registry == nil {
		return ErrInvalidConfig
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	if err := r.Registry.AuthorizePromotion(binding.Binding.PeerID, binding.Binding.PublicKey, binding.Binding.KeyEpoch, now); err != nil {
		return fmt.Errorf("authorize AWG binding promotion: %w", err)
	}
	return nil
}

var _ BindingDirectory = (*peerstore.Directory)(nil)
