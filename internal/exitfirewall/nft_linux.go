// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package exitfirewall

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"golang.org/x/sys/unix"

	"hydra.local/hydra/internal/linuxroute"
	"hydra.local/hydra/internal/safetypolicy"
	"hydra.local/hydra/internal/trafficstats"
)

const (
	inputChainName   = "input"
	forwardChainName = "forward"
	natChainName     = "postrouting"
	OwnerMarker      = "hydra-exit-firewall-v1"
)

var validObjectName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

// NFTBackend uses native netlink, never nft or iptables subprocesses.
type NFTBackend struct{}

// CleanupCrashResidue deletes only owned tables after the caller excludes a live TUN owner.
func CleanupCrashResidue(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidPolicy
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	table, err := findTable(DefaultTableName)
	if err != nil || table == nil {
		return err
	}
	owned, err := tableOwned(table)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("clean Exit table %s: %w", table.Name, ErrConflict)
	}
	return (&nftInstallation{table: table}).Close()
}

// Apply atomically creates the exclusive inet policy without partial installation.
func (NFTBackend) Apply(ctx context.Context, policy Policy) (Installation, error) {
	policy, err := normalizePolicy(policy)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, ErrInvalidPolicy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	table, err := findTable(policy.TableName)
	if err != nil {
		return nil, err
	}
	if table == nil {
		return installFresh(ctx, policy)
	}
	owned, err := tableOwned(table)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, fmt.Errorf("adopt nftables table %s: %w", policy.TableName, ErrConflict)
	}
	if err := reconcileTable(policy, table); err != nil {
		return nil, err
	}
	return &nftInstallation{table: table, tunName: policy.TUNName, policy: policy}, nil
}

func installFresh(ctx context.Context, policy Policy) (Installation, error) {
	conn := &nftables.Conn{}
	table := conn.CreateTable(&nftables.Table{Name: policy.TableName, Family: nftables.TableFamilyINet})
	accept := nftables.ChainPolicyAccept
	input := conn.AddChain(&nftables.Chain{
		Name: inputChainName, Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookInput, Priority: nftables.ChainPriorityFilter, Policy: &accept,
	})
	forward := conn.AddChain(&nftables.Chain{
		Name: forwardChainName, Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookForward, Priority: nftables.ChainPriorityFilter, Policy: &accept,
	})
	nat := conn.AddChain(&nftables.Chain{
		Name: natChainName, Table: table, Type: nftables.ChainTypeNAT,
		Hooknum: nftables.ChainHookPostrouting, Priority: nftables.ChainPriorityNATSource,
	})
	sets, err := safetypolicy.AddLinuxPortSets(conn, table)
	if err != nil {
		return nil, err
	}
	if err := trafficstats.Ensure(conn, table, true); err != nil {
		return nil, err
	}
	if err := trafficstats.Ensure(conn, table, true, trafficstats.DownloadCounterName); err != nil {
		return nil, err
	}
	for _, rule := range buildRules(policy, table, input, forward, nat, sets) {
		conn.AddRule(rule)
	}
	if err := conn.Flush(); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, fmt.Errorf("create nftables table %s: %w", policy.TableName, ErrConflict)
		}
		return nil, fmt.Errorf("apply Irvio Exit nftables policy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		cleanup := &nftInstallation{table: table}
		return nil, errors.Join(err, cleanup.Close())
	}
	return &nftInstallation{table: table, tunName: policy.TUNName, policy: policy}, nil
}

func reconcileTable(policy Policy, table *nftables.Table) error {
	chains, err := (&nftables.Conn{}).ListChains()
	if err != nil {
		return fmt.Errorf("list Irvio Exit chains: %w", err)
	}
	conn := &nftables.Conn{}
	var input, forward, nat *nftables.Chain
	for _, chain := range chains {
		if chain.Table == nil || chain.Table.Name != table.Name || chain.Table.Family != table.Family {
			continue
		}
		switch {
		case chain.Name == inputChainName && validFilterChain(chain, nftables.ChainHookInput):
			input = chain
			conn.FlushChain(chain)
		case chain.Name == forwardChainName && validFilterChain(chain, nftables.ChainHookForward):
			forward = chain
			conn.FlushChain(chain)
		case chain.Name == natChainName && validNATChain(chain):
			nat = chain
			conn.FlushChain(chain)
		default:
			conn.DelChain(chain)
		}
	}
	accept := nftables.ChainPolicyAccept
	if input == nil {
		input = conn.AddChain(&nftables.Chain{
			Name: inputChainName, Table: table, Type: nftables.ChainTypeFilter,
			Hooknum: nftables.ChainHookInput, Priority: nftables.ChainPriorityFilter, Policy: &accept,
		})
	}
	if forward == nil {
		forward = conn.AddChain(&nftables.Chain{
			Name: forwardChainName, Table: table, Type: nftables.ChainTypeFilter,
			Hooknum: nftables.ChainHookForward, Priority: nftables.ChainPriorityFilter, Policy: &accept,
		})
	}
	if nat == nil {
		nat = conn.AddChain(&nftables.Chain{
			Name: natChainName, Table: table, Type: nftables.ChainTypeNAT,
			Hooknum: nftables.ChainHookPostrouting, Priority: nftables.ChainPriorityNATSource,
		})
	}
	if err := safetypolicy.DeleteLinuxPortSets(conn, table); err != nil {
		return err
	}
	sets, err := safetypolicy.AddLinuxPortSets(conn, table)
	if err != nil {
		return err
	}
	if err := trafficstats.Ensure(conn, table, false); err != nil {
		return err
	}
	if err := trafficstats.Ensure(conn, table, false, trafficstats.DownloadCounterName); err != nil {
		return err
	}
	for _, rule := range buildRules(policy, table, input, forward, nat, sets) {
		conn.AddRule(rule)
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("atomically reconcile Irvio Exit nftables policy: %w", err)
	}
	return nil
}

func findTable(name string) (*nftables.Table, error) {
	tables, err := (&nftables.Conn{}).ListTables()
	if err != nil {
		return nil, fmt.Errorf("list nftables tables: %w", err)
	}
	for _, table := range tables {
		if table.Family == nftables.TableFamilyINet && table.Name == name {
			return table, nil
		}
	}
	return nil, nil
}

func tableOwned(table *nftables.Table) (bool, error) {
	conn := &nftables.Conn{}
	chains, err := conn.ListChains()
	if err != nil {
		return false, fmt.Errorf("list Irvio Exit chains: %w", err)
	}
	for _, chain := range chains {
		if chain.Table == nil || chain.Table.Name != table.Name || chain.Table.Family != table.Family {
			continue
		}
		rules, err := conn.GetRules(table, chain)
		if err != nil {
			return false, fmt.Errorf("read Irvio Exit ownership: %w", err)
		}
		for _, rule := range rules {
			label, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
			if ok && label == OwnerMarker {
				return true, nil
			}
		}
	}
	return false, nil
}

func validFilterChain(chain *nftables.Chain, hook *nftables.ChainHook) bool {
	return chain != nil && chain.Type == nftables.ChainTypeFilter && chain.Hooknum != nil && hook != nil &&
		*chain.Hooknum == *hook && chain.Priority != nil && *chain.Priority == *nftables.ChainPriorityFilter &&
		chain.Policy != nil && *chain.Policy == nftables.ChainPolicyAccept
}

func validNATChain(chain *nftables.Chain) bool {
	return chain != nil && chain.Type == nftables.ChainTypeNAT && chain.Hooknum != nil &&
		*chain.Hooknum == *nftables.ChainHookPostrouting && chain.Priority != nil &&
		*chain.Priority == *nftables.ChainPriorityNATSource
}

type nftInstallation struct {
	table   *nftables.Table
	tunName string
	policy  Policy

	mu               sync.Mutex
	closed           bool
	closeOnce        sync.Once
	closeErr         error
	finalBytes       uint64
	finalErr         error
	finalDownload    uint64
	finalDownloadErr error
}

func (i *nftInstallation) DownloadBytes() (uint64, error) {
	if !i.mu.TryLock() {
		return 0, errors.New("traffic counter busy")
	}
	defer i.mu.Unlock()
	if i.closed {
		return i.finalDownload, i.finalDownloadErr
	}
	return trafficstats.Read(i.table, trafficstats.DownloadCounterName)
}

func (i *nftInstallation) TrafficBytes() (uint64, error) {
	if !i.mu.TryLock() {
		return 0, errors.New("traffic counter busy")
	}
	defer i.mu.Unlock()
	if i.closed {
		return i.finalBytes, i.finalErr
	}
	return trafficstats.Read(i.table)
}

const localTunnelRulePrefix = "accept-local-upstream-"

// SetWANPath atomically replaces WAN rules while preserving safety, isolation, and INPUT ordering.
func (i *nftInstallation) SetWANPath(ctx context.Context, name string, ipv4, ipv6 netip.Addr) error {
	if i == nil || ctx == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return ErrClosed
	}
	next := i.policy
	next.WANName, next.WANIPv4, next.WANIPv6 = name, ipv4, ipv6
	var err error
	next, err = normalizePolicy(next)
	if err != nil {
		return err
	}
	conn := &nftables.Conn{}
	forward := &nftables.Chain{Name: forwardChainName, Table: i.table}
	forwardRules, err := conn.GetRules(i.table, forward)
	if err != nil {
		return fmt.Errorf("read Exit forwarding policy for WAN migration: %w", err)
	}
	forwardReplacements := map[string][]expr.Any{
		"accept-hydra-wan": append(append(ifaceMatch(expr.MetaKeyIIFNAME, next.TUNName), ifaceMatch(expr.MetaKeyOIFNAME, name)...), trafficstats.Reference(), &expr.Counter{}, &expr.Verdict{Kind: expr.VerdictAccept}),
		"accept-wan-reply": append(append(append(ifaceMatch(expr.MetaKeyIIFNAME, name), ifaceMatch(expr.MetaKeyOIFNAME, next.TUNName)...), conntrackEstablished()...), &expr.Counter{}, &expr.Verdict{Kind: expr.VerdictAccept}),
	}
	forwardSeen := make(map[string]bool, len(forwardReplacements))
	for index, rule := range forwardRules {
		label, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
		if !ok || forwardReplacements[label] == nil {
			continue
		}
		if forwardSeen[label] || rule.Handle == 0 || index+1 >= len(forwardRules) || forwardRules[index+1].Handle == 0 {
			return ErrConflict
		}
		forwardSeen[label] = true
		if err := conn.DelRule(rule); err != nil {
			return fmt.Errorf("replace Exit WAN forwarding rule %s: %w", label, err)
		}
		conn.InsertRule(&nftables.Rule{Table: i.table, Chain: forward,
			Position: forwardRules[index+1].Handle, Exprs: forwardReplacements[label], UserData: rule.UserData})
	}
	if len(forwardSeen) != len(forwardReplacements) {
		return ErrConflict
	}
	nat := &nftables.Chain{Name: natChainName, Table: i.table}
	natRules, err := conn.GetRules(i.table, nat)
	if err != nil {
		return fmt.Errorf("read Exit NAT policy for WAN migration: %w", err)
	}
	wantedNAT := make(map[string]*nftables.Rule)
	for _, rule := range buildNATRules(next, i.table, nat) {
		label, _ := userdata.GetString(rule.UserData, userdata.TypeComment)
		wantedNAT[label] = rule
	}
	natSeen := make(map[string]bool, len(wantedNAT))
	for _, rule := range natRules {
		label, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
		if !ok || wantedNAT[label] == nil {
			continue
		}
		if natSeen[label] || rule.Handle == 0 {
			return ErrConflict
		}
		natSeen[label] = true
		if err := conn.DelRule(rule); err != nil {
			return fmt.Errorf("replace Exit WAN NAT rule %s: %w", label, err)
		}
	}
	if len(natSeen) != len(wantedNAT) {
		return ErrConflict
	}
	for _, rule := range buildNATRules(next, i.table, nat) {
		conn.AddRule(rule)
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("atomically replace Exit WAN path policy: %w", err)
	}
	i.policy = next
	return nil
}

// SetNATAddresses atomically replaces owned NAT rules, preserving forwarding and non-NAT counters.
func (i *nftInstallation) SetNATAddresses(ctx context.Context, ipv4, ipv6 netip.Addr) error {
	if i == nil || ctx == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validOptionalWANAddress(ipv4, true) || !validOptionalWANAddress(ipv6, false) {
		return ErrInvalidPolicy
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return ErrClosed
	}
	next := i.policy
	next.WANIPv4, next.WANIPv6 = ipv4, ipv6
	conn := &nftables.Conn{}
	nat := &nftables.Chain{Name: natChainName, Table: i.table}
	rules, err := conn.GetRules(i.table, nat)
	if err != nil {
		return fmt.Errorf("read Irvio Exit NAT policy: %w", err)
	}
	for _, rule := range rules {
		label, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
		if ok && (label == "masquerade-ipv4" || label == "masquerade-ipv6") {
			if err := conn.DelRule(rule); err != nil {
				return fmt.Errorf("replace Irvio Exit NAT policy: %w", err)
			}
		}
	}
	for _, rule := range buildNATRules(next, i.table, nat) {
		conn.AddRule(rule)
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("replace Irvio Exit NAT policy: %w", err)
	}
	i.policy = next
	return nil
}

// SetLocalTunnelAddresses atomically changes local INPUT exceptions without removing the downstream drop.
func (i *nftInstallation) SetLocalTunnelAddresses(ctx context.Context, addresses []netip.Addr) error {
	if i == nil || ctx == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeLocalTunnelAddresses(addresses)
	if err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return ErrClosed
	}
	conn := &nftables.Conn{}
	input := &nftables.Chain{Name: inputChainName, Table: i.table}
	rules, err := conn.GetRules(i.table, input)
	if err != nil {
		return fmt.Errorf("read Irvio Exit input policy: %w", err)
	}
	for _, rule := range rules {
		label, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
		if ok && strings.HasPrefix(label, localTunnelRulePrefix) {
			if err := conn.DelRule(rule); err != nil {
				return fmt.Errorf("replace Irvio dual-role input policy: %w", err)
			}
		}
	}
	for index := len(normalized) - 1; index >= 0; index-- {
		address := normalized[index]
		expressions := ifaceMatch(expr.MetaKeyIIFNAME, i.tunName)
		expressions = append(expressions, addressMatch(netip.PrefixFrom(address, address.BitLen()), false)...)
		expressions = append(expressions, conntrackEstablished()...)
		// Count only local upstream replies without verdicts; restored socket marks exclude control traffic.
		accounting := append([]expr.Any(nil), expressions...)
		accounting = append(accounting,
			&expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(linuxroute.IrvioBypassMark)},
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(linuxroute.IrvioTunnelControlMark)},
			trafficstats.DownloadReference())
		expressions = append(expressions, &expr.Counter{}, &expr.Verdict{Kind: expr.VerdictAccept})
		rule := &nftables.Rule{Table: i.table, Chain: input, Exprs: expressions}
		rule.UserData = userdata.AppendString(nil, userdata.TypeComment, fmt.Sprintf("%s%02d", localTunnelRulePrefix, index))
		conn.InsertRule(rule)
		counterRule := &nftables.Rule{Table: i.table, Chain: input, Exprs: accounting}
		counterRule.UserData = userdata.AppendString(nil, userdata.TypeComment, fmt.Sprintf("%sdownload-%02d", localTunnelRulePrefix, index))
		conn.InsertRule(counterRule)
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("replace Irvio dual-role input policy: %w", err)
	}
	return nil
}

func normalizeLocalTunnelAddresses(addresses []netip.Addr) ([]netip.Addr, error) {
	result := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		if !address.IsValid() || address.Is4In6() || address.IsUnspecified() || address.IsMulticast() || address.IsLoopback() || address.IsLinkLocalUnicast() {
			return nil, ErrInvalidPolicy
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	slices.SortFunc(result, func(a, b netip.Addr) int { return a.Compare(b) })
	return result, nil
}

func (i *nftInstallation) Snapshot(ctx context.Context) (Snapshot, error) {
	if i == nil || ctx == nil {
		return Snapshot{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	i.mu.Lock()
	closed := i.closed
	i.mu.Unlock()
	if closed {
		return Snapshot{}, ErrClosed
	}
	conn := &nftables.Conn{}
	result := Snapshot{TableName: i.table.Name, Counters: make(map[string]Counter)}
	for _, chainName := range []string{inputChainName, forwardChainName, natChainName} {
		chain := &nftables.Chain{Name: chainName, Table: i.table}
		rules, err := conn.GetRules(i.table, chain)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read nftables chain %s: %w", chainName, err)
		}
		result.Rules += len(rules)
		for _, rule := range rules {
			label, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
			if !ok {
				continue
			}
			for _, expression := range rule.Exprs {
				counter, ok := expression.(*expr.Counter)
				if ok {
					result.Counters[label] = Counter{Packets: counter.Packets, Bytes: counter.Bytes}
				}
			}
		}
	}
	return result, nil
}

func (i *nftInstallation) Close() error {
	if i == nil {
		return nil
	}
	i.closeOnce.Do(func() {
		i.mu.Lock()
		i.finalBytes, i.finalErr = trafficstats.Read(i.table)
		i.finalDownload, i.finalDownloadErr = trafficstats.Read(i.table, trafficstats.DownloadCounterName)
		i.closed = true
		i.mu.Unlock()
		conn := &nftables.Conn{}
		conn.DelTable(i.table)
		if err := conn.Flush(); err != nil && !errors.Is(err, unix.ENOENT) {
			i.closeErr = fmt.Errorf("remove Irvio Exit nftables table: %w", err)
		}
	})
	return i.closeErr
}

func normalizePolicy(policy Policy) (Policy, error) {
	if policy.TableName == "" {
		policy.TableName = DefaultTableName
	}
	if !validObjectName.MatchString(policy.TableName) || !validObjectName.MatchString(policy.TUNName) || !validObjectName.MatchString(policy.WANName) || policy.TUNName == policy.WANName {
		return Policy{}, ErrInvalidPolicy
	}
	if !validClientPrefix(policy.ClientIPv4, true) && !validClientPrefix(policy.ClientIPv6, false) {
		return Policy{}, ErrInvalidPolicy
	}
	if policy.ClientIPv4.IsValid() && !validClientPrefix(policy.ClientIPv4, true) || policy.ClientIPv6.IsValid() && !validClientPrefix(policy.ClientIPv6, false) {
		return Policy{}, ErrInvalidPolicy
	}
	if !validOptionalWANAddress(policy.WANIPv4, true) || !validOptionalWANAddress(policy.WANIPv6, false) {
		return Policy{}, ErrInvalidPolicy
	}
	seenControl := make(map[netip.AddrPort]struct{}, len(policy.SessionControl))
	control := policy.SessionControl[:0]
	for _, endpoint := range policy.SessionControl {
		address := endpoint.Addr()
		if !endpoint.IsValid() || endpoint.Port() == 0 || address.Is4In6() || address.IsUnspecified() ||
			address.IsMulticast() || address.IsLoopback() || address.IsLinkLocalUnicast() {
			return Policy{}, ErrInvalidPolicy
		}
		if _, duplicate := seenControl[endpoint]; duplicate {
			continue
		}
		seenControl[endpoint] = struct{}{}
		control = append(control, endpoint)
	}
	policy.SessionControl = control
	slices.SortFunc(policy.SessionControl, func(a, b netip.AddrPort) int { return a.Compare(b) })
	blocked := append(builtinBlockedPrefixes(), policy.BlockedDestinations...)
	seen := make(map[netip.Prefix]struct{}, len(blocked))
	policy.BlockedDestinations = policy.BlockedDestinations[:0]
	for _, prefix := range blocked {
		if !prefix.IsValid() || prefix.Addr().Is4In6() || prefix.Bits() == 0 {
			return Policy{}, ErrInvalidPolicy
		}
		prefix = prefix.Masked()
		if _, exists := seen[prefix]; exists {
			continue
		}
		seen[prefix] = struct{}{}
		policy.BlockedDestinations = append(policy.BlockedDestinations, prefix)
	}
	slices.SortFunc(policy.BlockedDestinations, func(a, b netip.Prefix) int {
		if comparison := a.Addr().Compare(b.Addr()); comparison != 0 {
			return comparison
		}
		return a.Bits() - b.Bits()
	})
	return policy, nil
}

func validOptionalWANAddress(address netip.Addr, ipv4 bool) bool {
	if !address.IsValid() {
		return true
	}
	return !address.Is4In6() && address.Is4() == ipv4 && !address.IsUnspecified() &&
		!address.IsMulticast() && !address.IsLoopback() && !address.IsLinkLocalUnicast()
}

func validClientPrefix(prefix netip.Prefix, ipv4 bool) bool {
	return prefix.IsValid() && !prefix.Addr().Is4In6() && prefix.Addr().Is4() == ipv4 && !prefix.Addr().IsUnspecified() && !prefix.Addr().IsMulticast()
}

func builtinBlockedPrefixes() []netip.Prefix {
	values := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16",
		"::1/128", "fc00::/7", "fe80::/10",
	}
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func buildRules(policy Policy, table *nftables.Table, input, forward, nat *nftables.Chain, sets safetypolicy.LinuxPortSets) []*nftables.Rule {
	rules := make([]*nftables.Rule, 0, len(policy.BlockedDestinations)+8)
	add := func(chain *nftables.Chain, label string, expressions ...expr.Any) {
		// Place counters before terminating verdict and NAT expressions.
		if len(expressions) == 0 {
			expressions = []expr.Any{&expr.Counter{}}
		} else {
			last := len(expressions) - 1
			expressions = append(expressions, nil)
			copy(expressions[last+1:], expressions[last:])
			expressions[last] = &expr.Counter{}
		}
		rule := &nftables.Rule{Table: table, Chain: chain, Exprs: expressions}
		rule.UserData = userdata.AppendString(nil, userdata.TypeComment, label)
		rules = append(rules, rule)
	}
	add(input, OwnerMarker)
	for index, endpoint := range policy.SessionControl {
		expressions := ifaceMatch(expr.MetaKeyIIFNAME, policy.TUNName)
		expressions = append(expressions, addressMatch(netip.PrefixFrom(endpoint.Addr(), endpoint.Addr().BitLen()), false)...)
		expressions = append(expressions, tcpDestinationPortMatch(endpoint.Port())...)
		expressions = append(expressions, &expr.Verdict{Kind: expr.VerdictAccept})
		add(input, fmt.Sprintf("accept-session-control-%02d", index), expressions...)
	}
	add(input, "drop-hydra-to-exit", append(ifaceMatch(expr.MetaKeyIIFNAME, policy.TUNName), &expr.Verdict{Kind: expr.VerdictDrop})...)
	add(forward, "drop-hydra-multihop", append(append(ifaceMatch(expr.MetaKeyIIFNAME, policy.TUNName), ifaceMatch(expr.MetaKeyOIFNAME, policy.TUNName)...), &expr.Verdict{Kind: expr.VerdictDrop})...)
	for index, prefix := range policy.BlockedDestinations {
		expressions := ifaceMatch(expr.MetaKeyIIFNAME, policy.TUNName)
		expressions = append(expressions, addressMatch(prefix, false)...)
		expressions = append(expressions, &expr.Verdict{Kind: expr.VerdictDrop})
		add(forward, fmt.Sprintf("drop-local-%02d", index), expressions...)
	}
	for _, item := range []struct {
		label     string
		transport safetypolicy.Transport
	}{
		{"drop-safety-v0-tcp", safetypolicy.TransportTCP},
		{"drop-safety-v0-udp", safetypolicy.TransportUDP},
	} {
		expressions := ifaceMatch(expr.MetaKeyIIFNAME, policy.TUNName)
		expressions = append(expressions, safetypolicy.LinuxDestinationPortMatch(item.transport, sets)...)
		expressions = append(expressions, &expr.Verdict{Kind: expr.VerdictDrop})
		add(forward, item.label, expressions...)
	}
	add(forward, "accept-hydra-wan", append(append(ifaceMatch(expr.MetaKeyIIFNAME, policy.TUNName), ifaceMatch(expr.MetaKeyOIFNAME, policy.WANName)...), trafficstats.Reference(), &expr.Verdict{Kind: expr.VerdictAccept})...)
	add(forward, "drop-hydra-other", append(ifaceMatch(expr.MetaKeyIIFNAME, policy.TUNName), &expr.Verdict{Kind: expr.VerdictDrop})...)
	reply := append(ifaceMatch(expr.MetaKeyIIFNAME, policy.WANName), ifaceMatch(expr.MetaKeyOIFNAME, policy.TUNName)...)
	reply = append(reply, conntrackEstablished()...)
	reply = append(reply, &expr.Verdict{Kind: expr.VerdictAccept})
	add(forward, "accept-wan-reply", reply...)
	add(forward, "drop-to-hydra", append(ifaceMatch(expr.MetaKeyOIFNAME, policy.TUNName), &expr.Verdict{Kind: expr.VerdictDrop})...)
	rules = append(rules, buildNATRules(policy, table, nat)...)
	return rules
}

func tcpDestinationPortMatch(port uint16) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(port)},
	}
}

func buildNATRules(policy Policy, table *nftables.Table, nat *nftables.Chain) []*nftables.Rule {
	rules := make([]*nftables.Rule, 0, 2)
	for _, item := range []struct {
		prefix  netip.Prefix
		address netip.Addr
		label   string
	}{{policy.ClientIPv4, policy.WANIPv4, "masquerade-ipv4"}, {policy.ClientIPv6, policy.WANIPv6, "masquerade-ipv6"}} {
		if !item.prefix.IsValid() {
			continue
		}
		expressions := addressMatch(item.prefix, true)
		expressions = append(expressions, ifaceMatch(expr.MetaKeyOIFNAME, policy.WANName)...)
		if item.address.IsValid() {
			expressions = append(expressions,
				&expr.Immediate{Register: 1, Data: item.address.AsSlice()},
				&expr.NAT{Type: expr.NATTypeSourceNAT, Family: uint32(familyByte(item.address)), RegAddrMin: 1},
			)
		} else {
			expressions = append(expressions, &expr.Masq{})
		}
		last := len(expressions) - 1
		expressions = append(expressions, nil)
		copy(expressions[last+1:], expressions[last:])
		expressions[last] = &expr.Counter{}
		rule := &nftables.Rule{Table: table, Chain: nat, Exprs: expressions}
		rule.UserData = userdata.AppendString(nil, userdata.TypeComment, item.label)
		rules = append(rules, rule)
	}
	return rules
}

func ifaceMatch(key expr.MetaKey, name string) []expr.Any {
	data := make([]byte, unix.IFNAMSIZ)
	copy(data, name)
	return []expr.Any{
		&expr.Meta{Key: key, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: data},
	}
}

func addressMatch(prefix netip.Prefix, source bool) []expr.Any {
	prefix = prefix.Masked()
	addr := prefix.Addr()
	offset := uint32(16)
	if source {
		offset = 12
	}
	if addr.Is6() {
		offset = 24
		if source {
			offset = 8
		}
	}
	bytes := addr.AsSlice()
	result := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{familyByte(addr)}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: uint32(len(bytes))},
	}
	if prefix.Bits() != addr.BitLen() {
		mask := []byte(net.CIDRMask(prefix.Bits(), addr.BitLen()))
		result = append(result, &expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: uint32(len(bytes)), Mask: mask, Xor: make([]byte, len(bytes))})
	}
	result = append(result, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: bytes})
	return result
}

func familyByte(addr netip.Addr) byte {
	if addr.Is4() {
		return unix.NFPROTO_IPV4
	}
	return unix.NFPROTO_IPV6
}

func conntrackEstablished() []expr.Any {
	states := expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED
	return []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: binaryutil.NativeEndian.PutUint32(states), Xor: binaryutil.NativeEndian.PutUint32(0)},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
	}
}
