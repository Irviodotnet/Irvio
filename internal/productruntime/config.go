// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Irviodotnet/Irvio/internal/productbootstrap"
)

const DefaultConfigPath = "/etc/irvio/config.json"

// ServerUDPPort is stable across restarts so installation-time firewall rules remain valid.
const ServerUDPPort = 51820

type Role string

const (
	Desktop Role = "desktop"
	Server  Role = "server"
)

type FileConfig struct {
	Version int  `json:"version"`
	Role    Role `json:"role,omitempty"`
}

func ParseRole(value string) (Role, error) {
	role := Role(value)
	if role != Desktop && role != Server {
		return "", fmt.Errorf("invalid product role %q: expected desktop or server", value)
	}
	return role, nil
}

// LoadConfig accepts versioned static intent only, never dynamic facts, learned peers, or secrets.
func LoadConfig(path string) (FileConfig, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return FileConfig{Version: 1}, nil
	}
	if err != nil {
		return FileConfig{}, fmt.Errorf("open product config: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return FileConfig{}, fmt.Errorf("stat product config: %w", err)
	}
	if info.Size() > 4096 {
		return FileConfig{}, fmt.Errorf("product config exceeds 4096 bytes")
	}
	contents, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return FileConfig{}, fmt.Errorf("read product config: %w", err)
	}
	if len(contents) > 4096 {
		return FileConfig{}, fmt.Errorf("product config exceeds 4096 bytes")
	}
	if err := rejectDuplicateFields(contents); err != nil {
		return FileConfig{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var config FileConfig
	if err := decoder.Decode(&config); err != nil {
		return FileConfig{}, fmt.Errorf("decode product config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return FileConfig{}, fmt.Errorf("product config has trailing data or exceeds 4096 bytes")
	}
	if config.Version != 1 {
		return FileConfig{}, fmt.Errorf("unsupported product config version %d", config.Version)
	}
	if config.Role != "" {
		if _, err := ParseRole(string(config.Role)); err != nil {
			return FileConfig{}, err
		}
	}
	return config, nil
}

func rejectDuplicateFields(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return fmt.Errorf("product config must be a JSON object")
	}
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode product config field: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("product config field name is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate product config field %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("decode product config field %q: %w", key, err)
		}
		if bytes.Equal(value, []byte("null")) {
			return fmt.Errorf("product config field %q cannot be null", key)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("close product config object: %w", err)
	}
	return nil
}

// ResolveRole prioritizes explicit CLI intent over config and defaults to desktop.
func ResolveRole(config FileConfig, cliRole string) (Role, error) {
	if cliRole != "" {
		return ParseRole(cliRole)
	}
	if config.Role != "" {
		return ParseRole(string(config.Role))
	}
	return Desktop, nil
}

// ExpandBootstrap validates P2.1 bundles and preserves all peers alongside legacy multiaddrs.
func ExpandBootstrap(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, "irvio-bootstrap:") {
			bundle, err := productbootstrap.Decode(value)
			if err != nil {
				return nil, fmt.Errorf("--bootstrap bundle: %w", err)
			}
			addresses, err := productbootstrap.Multiaddrs(bundle)
			if err != nil {
				return nil, fmt.Errorf("--bootstrap bundle addresses: %w", err)
			}
			result = append(result, addresses...)
			continue
		}
		result = append(result, value)
	}
	return result, nil
}
