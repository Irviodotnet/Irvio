// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productbootstrap

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"

	"hydra.local/hydra/internal/bootstrap"
	localv1 "hydra.local/hydra/proto/hydra/local/v1"
)

const (
	BundleVersion       uint32 = 1
	BundlePrefix               = "irvio-bootstrap:v1:"
	MaxBundleNodes             = 32
	MaxEndpointsPerNode        = 4
	MaxBundleBytes             = 16 * 1024
	MaxBundleCodeBytes         = 24 * 1024
)

var (
	ErrInvalidBundle     = errors.New("invalid bootstrap bundle")
	ErrUnsupportedBundle = errors.New("unsupported bootstrap bundle version")
	ErrBundleTooLarge    = errors.New("bootstrap bundle is too large")
	ErrNoBootstrapNodes  = errors.New("bootstrap bundle contains no valid nodes")
)

func Decode(code string) (*localv1.BootstrapBundle, error) {
	code = strings.TrimSpace(code)
	if len(code) > MaxBundleCodeBytes {
		return nil, ErrBundleTooLarge
	}
	if !strings.HasPrefix(code, BundlePrefix) {
		if strings.HasPrefix(code, "irvio-bootstrap:") {
			return nil, ErrUnsupportedBundle
		}
		return nil, ErrInvalidBundle
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, BundlePrefix))
	if err != nil {
		return nil, fmt.Errorf("%w: base64url", ErrInvalidBundle)
	}
	return decodeBytes(raw)
}

func Encode(bundle *localv1.BootstrapBundle) (string, error) {
	normalized, err := Normalize(bundle)
	if err != nil {
		return "", err
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal bootstrap bundle: %w", err)
	}
	if len(raw) == 0 || len(raw) > MaxBundleBytes {
		return "", ErrBundleTooLarge
	}
	code := BundlePrefix + base64.RawURLEncoding.EncodeToString(raw)
	if len(code) > MaxBundleCodeBytes {
		return "", ErrBundleTooLarge
	}
	return code, nil
}

func DecodeStored(raw []byte) (*localv1.BootstrapBundle, error) {
	return decodeBytes(raw)
}

func EncodeStored(bundle *localv1.BootstrapBundle) ([]byte, error) {
	normalized, err := Normalize(bundle)
	if err != nil {
		return nil, err
	}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal persisted bootstrap bundle: %w", err)
	}
	if len(raw) == 0 || len(raw) > MaxBundleBytes {
		return nil, ErrBundleTooLarge
	}
	return raw, nil
}

func decodeBytes(raw []byte) (*localv1.BootstrapBundle, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidBundle
	}
	if len(raw) > MaxBundleBytes {
		return nil, ErrBundleTooLarge
	}
	bundle := new(localv1.BootstrapBundle)
	if err := (proto.UnmarshalOptions{RecursionLimit: 8}).Unmarshal(raw, bundle); err != nil {
		return nil, fmt.Errorf("%w: protobuf", ErrInvalidBundle)
	}
	return Normalize(bundle)
}

func Normalize(bundle *localv1.BootstrapBundle) (*localv1.BootstrapBundle, error) {
	if bundle == nil {
		return nil, ErrInvalidBundle
	}
	if bundle.FormatVersion != BundleVersion {
		return nil, ErrUnsupportedBundle
	}
	if len(bundle.Nodes) == 0 {
		return nil, ErrNoBootstrapNodes
	}
	if len(bundle.Nodes) > MaxBundleNodes {
		return nil, ErrBundleTooLarge
	}
	byPeer := make(map[peer.ID]map[netip.AddrPort]struct{}, len(bundle.Nodes))
	for _, node := range bundle.Nodes {
		if node == nil || len(node.PeerId) == 0 || len(node.PeerId) > 128 || len(node.Endpoints) == 0 || len(node.Endpoints) > MaxEndpointsPerNode {
			return nil, ErrInvalidBundle
		}
		id, err := peer.Decode(node.PeerId)
		if err != nil || id.Validate() != nil {
			return nil, fmt.Errorf("%w: PeerID", ErrInvalidBundle)
		}
		endpoints := byPeer[id]
		if endpoints == nil {
			endpoints = make(map[netip.AddrPort]struct{}, len(node.Endpoints))
			byPeer[id] = endpoints
		}
		for _, endpoint := range node.Endpoints {
			if endpoint == nil || endpoint.Port == 0 || endpoint.Port > 65535 {
				return nil, fmt.Errorf("%w: endpoint port", ErrInvalidBundle)
			}
			address, ok := netip.AddrFromSlice(endpoint.Ip)
			if !ok || !address.IsValid() || address.Is4In6() || address.IsUnspecified() || address.IsMulticast() {
				return nil, fmt.Errorf("%w: literal IP endpoint", ErrInvalidBundle)
			}
			value := netip.AddrPortFrom(address, uint16(endpoint.Port))
			if _, duplicate := endpoints[value]; duplicate {
				continue
			}
			if len(endpoints) == MaxEndpointsPerNode {
				return nil, ErrBundleTooLarge
			}
			endpoints[value] = struct{}{}
		}
	}
	ids := make([]peer.ID, 0, len(byPeer))
	for id := range byPeer {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(left, right peer.ID) int { return strings.Compare(left.String(), right.String()) })
	result := &localv1.BootstrapBundle{FormatVersion: BundleVersion, GeneratedAtUnixMs: bundle.GeneratedAtUnixMs}
	for _, id := range ids {
		values := make([]netip.AddrPort, 0, len(byPeer[id]))
		for endpoint := range byPeer[id] {
			values = append(values, endpoint)
		}
		slices.SortFunc(values, func(left, right netip.AddrPort) int { return strings.Compare(left.String(), right.String()) })
		node := &localv1.BootstrapNode{PeerId: id.String(), Endpoints: make([]*localv1.BootstrapEndpoint, 0, len(values))}
		for _, endpoint := range values {
			node.Endpoints = append(node.Endpoints, &localv1.BootstrapEndpoint{
				Ip: append([]byte(nil), endpoint.Addr().AsSlice()...), Port: uint32(endpoint.Port()),
			})
		}
		result.Nodes = append(result.Nodes, node)
	}
	return result, nil
}

func Multiaddrs(bundle *localv1.BootstrapBundle) ([]string, error) {
	normalized, err := Normalize(bundle)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(normalized.Nodes)*2)
	for _, node := range normalized.Nodes {
		for _, endpoint := range node.Endpoints {
			address, _ := netip.AddrFromSlice(endpoint.Ip)
			family := "ip6"
			if address.Is4() {
				family = "ip4"
			}
			result = append(result, fmt.Sprintf("/%s/%s/tcp/%d/p2p/%s", family, address, endpoint.Port, node.PeerId))
		}
	}
	return result, nil
}

func FromMultiaddrs(addresses []string, generatedAtUnixMs uint64) (*localv1.BootstrapBundle, error) {
	infos, err := bootstrap.Parse(addresses)
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, ErrNoBootstrapNodes
	}
	bundle := &localv1.BootstrapBundle{FormatVersion: BundleVersion, GeneratedAtUnixMs: generatedAtUnixMs}
	for _, info := range infos {
		node := &localv1.BootstrapNode{PeerId: info.ID.String()}
		for _, address := range info.Addrs {
			ip, port, ok := literalTCPEndpoint(address)
			if !ok {
				return nil, fmt.Errorf("%w: bootstrap endpoint must be literal IP/TCP", ErrInvalidBundle)
			}
			node.Endpoints = append(node.Endpoints, &localv1.BootstrapEndpoint{Ip: ip.AsSlice(), Port: uint32(port)})
		}
		bundle.Nodes = append(bundle.Nodes, node)
	}
	return Normalize(bundle)
}

func literalTCPEndpoint(address ma.Multiaddr) (netip.Addr, uint16, bool) {
	value, err := address.ValueForProtocol(ma.P_IP4)
	if err != nil {
		value, err = address.ValueForProtocol(ma.P_IP6)
	}
	if err != nil {
		return netip.Addr{}, 0, false
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Is4In6() {
		return netip.Addr{}, 0, false
	}
	portValue, err := address.ValueForProtocol(ma.P_TCP)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	port, err := strconv.ParseUint(portValue, 10, 16)
	if err != nil || port == 0 {
		return netip.Addr{}, 0, false
	}
	return ip, uint16(port), true
}
