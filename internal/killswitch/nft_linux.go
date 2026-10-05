// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package killswitch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
	preroutingChainName = "prerouting_v1"
	outputChainName     = "output_v1"

	sourceValidationPreviousZero = "source-validation-previous-0"
	sourceValidationPreviousOne  = "source-validation-previous-1"
)

var validObjectName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

var expectedRuleLabels = []string{
	OwnerMarker,
	"mark-inbound-bypass",
	"mark-inbound-tunnel-control",
	"accept-bypass",
	"accept-loopback",
	"reject-safety-v0-tcp",
	"reject-safety-v0-udp",
	"account-user-upload",
	"accept-tunnel",
	"accept-dhcp4",
	"accept-dhcp6",
	"accept-icmpv6-rs",
	"accept-icmpv6-ra",
	"accept-icmpv6-ns",
	"accept-icmpv6-na",
	"drop-ordinary",
}

// NFTBackend persists policy in the network namespace until explicit Remove.
type NFTBackend struct{}

func (NFTBackend) Ensure(ctx context.Context, policy Policy) (Installation, EnsureResult, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, 0, ErrInvalidConfig
	}
	policy, err := normalizePolicy(policy)
	if err != nil {
		return nil, 0, err
	}
	existing, err := findTable(policy.TableName)
	if err != nil {
		return nil, 0, err
	}
	if existing == nil {
		installation, err := installFresh(policy)
		return installation, EnsureInstalled, err
	}
	owned, complete, versionErr, err := inspectOwnership(existing)
	if err != nil {
		return nil, 0, err
	}
	if versionErr != nil {
		return nil, 0, versionErr
	}
	if !owned {
		return nil, 0, ErrConflict
	}
	sourceValidationEnabled, err := readSourceValidation()
	if err != nil {
		return nil, 0, err
	}
	complete = complete && sourceValidationEnabled
	previous, err := sourceValidationOwnership(existing)
	if err != nil {
		return nil, 0, err
	}
	if previous == nil {
		value, readErr := readSourceValidation()
		if readErr != nil {
			return nil, 0, readErr
		}
		previous = &value
	}
	installation := &nftInstallation{policy: policy, table: existing, sourceValidationPrevious: *previous}
	if err := installation.reconcile(); err != nil {
		return nil, 0, err
	}
	if complete {
		return installation, EnsureAdopted, nil
	}
	return installation, EnsureReconciled, nil
}

func (NFTBackend) Inspect(ctx context.Context, policy Policy) (KernelSnapshot, error) {
	if ctx == nil || ctx.Err() != nil {
		return KernelSnapshot{}, ErrInvalidConfig
	}
	policy, err := normalizePolicy(policy)
	if err != nil {
		return KernelSnapshot{}, err
	}
	table, err := findTable(policy.TableName)
	if err != nil || table == nil {
		return KernelSnapshot{}, err
	}
	previous, err := sourceValidationOwnership(table)
	if err != nil {
		return KernelSnapshot{}, err
	}
	if previous == nil {
		value, readErr := readSourceValidation()
		if readErr != nil {
			return KernelSnapshot{}, readErr
		}
		previous = &value
	}
	installation := &nftInstallation{policy: policy, table: table, sourceValidationPrevious: *previous}
	return installation.Snapshot(ctx)
}

type nftInstallation struct {
	policy Policy
	table  *nftables.Table
	// Persist sourceValidationPrevious in an owned no-op rule for restoration after crash adoption.
	sourceValidationPrevious bool

	mu         sync.Mutex
	removed    bool
	finalBytes uint64
	finalErr   error
}

// TrafficBytes reads only accounting state, not the cached policy snapshot.
func (i *nftInstallation) TrafficBytes() (uint64, error) {
	if !i.mu.TryLock() {
		return 0, errors.New("traffic counter busy")
	}
	defer i.mu.Unlock()
	if i.removed {
		return i.finalBytes, i.finalErr
	}
	return trafficstats.Read(i.table)
}

func (i *nftInstallation) Snapshot(ctx context.Context) (KernelSnapshot, error) {
	if i == nil || ctx == nil {
		return KernelSnapshot{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return KernelSnapshot{}, err
	}
	i.mu.Lock()
	removed := i.removed
	i.mu.Unlock()
	if removed {
		return KernelSnapshot{}, nil
	}
	table, err := findTable(i.policy.TableName)
	if err != nil || table == nil {
		return KernelSnapshot{}, err
	}
	labels, counters, err := tableRuleLabels(table)
	if err != nil {
		return KernelSnapshot{}, err
	}
	owned, versionErr := labelOwnership(labels)
	if versionErr != nil {
		return KernelSnapshot{}, versionErr
	}
	if !owned {
		return KernelSnapshot{}, ErrConflict
	}
	sourceValidationEnabled, err := readSourceValidation()
	if err != nil {
		return KernelSnapshot{}, err
	}
	setsComplete, err := safetypolicy.LinuxPortSetsComplete(table)
	if err != nil {
		return KernelSnapshot{}, err
	}
	return KernelSnapshot{
		TableName: table.Name, Version: PolicyVersion, Installed: true,
		Complete: labelsComplete(labels) && setsComplete && sourceValidationEnabled, Counters: counters,
		SourceValidationEnabled: sourceValidationEnabled,
	}, nil
}

func (i *nftInstallation) Reconcile(ctx context.Context) error {
	if i == nil || ctx == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.removed {
		return ErrNotProtected
	}
	return i.reconcile()
}

func (i *nftInstallation) reconcile() error {
	table, err := findTable(i.policy.TableName)
	if err != nil || table == nil {
		return errors.Join(ErrNotProtected, err)
	}
	owned, _, versionErr, err := inspectOwnership(table)
	if err != nil {
		return err
	}
	if versionErr != nil {
		return versionErr
	}
	if !owned {
		return ErrConflict
	}
	chains, err := (&nftables.Conn{}).ListChains()
	if err != nil {
		return fmt.Errorf("list Irvio protection chains: %w", err)
	}
	conn := &nftables.Conn{}
	var prerouting, output *nftables.Chain
	for _, existing := range chains {
		if existing.Table == nil || existing.Table.Name != table.Name || existing.Table.Family != table.Family {
			continue
		}
		switch {
		case existing.Name == preroutingChainName && validPreroutingChain(existing):
			prerouting = existing
			conn.FlushChain(existing)
		case existing.Name == outputChainName && validOutputChain(existing):
			output = existing
			conn.FlushChain(existing)
		default:
			conn.DelChain(existing)
		}
	}
	if prerouting == nil {
		accept := nftables.ChainPolicyAccept
		prerouting = conn.AddChain(&nftables.Chain{
			Name: preroutingChainName, Table: table, Type: nftables.ChainTypeFilter,
			Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityMangle, Policy: &accept,
		})
	}
	if output == nil {
		drop := nftables.ChainPolicyDrop
		output = conn.AddChain(&nftables.Chain{
			Name: outputChainName, Table: table, Type: nftables.ChainTypeFilter,
			Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityFilter, Policy: &drop,
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
	for _, rule := range buildPreroutingRules(i.policy, table, prerouting) {
		conn.AddRule(rule)
	}
	for _, rule := range buildRules(i.policy, table, output, sets, i.sourceValidationPrevious) {
		conn.AddRule(rule)
	}
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("atomically reconcile Irvio protection: %w", err)
	}
	if err := writeSourceValidation(true); err != nil {
		return err
	}
	i.table = table
	return nil
}

func (i *nftInstallation) Remove(ctx context.Context) error {
	if i == nil || ctx == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.removed {
		return nil
	}
	table, err := findTable(i.policy.TableName)
	if err != nil {
		return err
	}
	if table == nil {
		if err := writeSourceValidation(i.sourceValidationPrevious); err != nil {
			return err
		}
		i.removed = true
		return nil
	}
	owned, _, versionErr, err := inspectOwnership(table)
	if err != nil {
		return err
	}
	if versionErr != nil {
		return versionErr
	}
	if !owned {
		return ErrConflict
	}
	if err := writeSourceValidation(i.sourceValidationPrevious); err != nil {
		return err
	}
	conn := &nftables.Conn{}
	conn.DelTable(table)
	i.finalBytes, i.finalErr = trafficstats.Read(table)
	if err := conn.Flush(); err != nil && !errors.Is(err, unix.ENOENT) {
		_ = writeSourceValidation(true)
		return fmt.Errorf("remove Irvio protection: %w", err)
	}
	i.removed = true
	return nil
}

func normalizePolicy(policy Policy) (Policy, error) {
	if policy.TableName == "" {
		policy.TableName = DefaultTableName
	}
	if policy.TunnelControlMark == 0 {
		policy.TunnelControlMark = linuxroute.IrvioTunnelControlMark
	}
	if !validObjectName.MatchString(policy.TableName) || !validObjectName.MatchString(policy.TUNName) ||
		policy.BypassMark == 0 || policy.TunnelControlMark == 0 || policy.TunnelControlMark == policy.BypassMark {
		return Policy{}, ErrInvalidConfig
	}
	return policy, nil
}

func installFresh(policy Policy) (*nftInstallation, error) {
	previous, err := readSourceValidation()
	if err != nil {
		return nil, err
	}
	conn := &nftables.Conn{}
	table := conn.CreateTable(&nftables.Table{Name: policy.TableName, Family: nftables.TableFamilyINet})
	accept := nftables.ChainPolicyAccept
	prerouting := conn.AddChain(&nftables.Chain{
		Name: preroutingChainName, Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityMangle, Policy: &accept,
	})
	drop := nftables.ChainPolicyDrop
	output := conn.AddChain(&nftables.Chain{
		Name: outputChainName, Table: table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityFilter, Policy: &drop,
	})
	sets, err := safetypolicy.AddLinuxPortSets(conn, table)
	if err != nil {
		return nil, err
	}
	if err := trafficstats.Ensure(conn, table, true); err != nil {
		return nil, err
	}
	for _, rule := range buildPreroutingRules(policy, table, prerouting) {
		conn.AddRule(rule)
	}
	for _, rule := range buildRules(policy, table, output, sets, previous) {
		conn.AddRule(rule)
	}
	if err := conn.Flush(); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("atomically install Irvio protection: %w", err)
	}
	if err := writeSourceValidation(true); err != nil {
		rollback := &nftables.Conn{}
		rollback.DelTable(table)
		return nil, errors.Join(err, rollback.Flush(), writeSourceValidation(previous))
	}
	return &nftInstallation{policy: policy, table: table, sourceValidationPrevious: previous}, nil
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

func inspectOwnership(table *nftables.Table) (bool, bool, error, error) {
	labels, _, err := tableRuleLabels(table)
	if err != nil {
		return false, false, nil, err
	}
	owned, versionErr := labelOwnership(labels)
	if !owned || versionErr != nil {
		return owned, false, versionErr, nil
	}
	setsComplete, err := safetypolicy.LinuxPortSetsComplete(table)
	return owned, labelsComplete(labels) && setsComplete, nil, err
}

func labelOwnership(labels []string) (bool, error) {
	for _, label := range labels {
		if label == OwnerMarker {
			return true, nil
		}
		if len(label) >= len("hydra-protected-mode-") && label[:len("hydra-protected-mode-")] == "hydra-protected-mode-" {
			return false, ErrVersionMismatch
		}
	}
	return false, nil
}

func labelsComplete(labels []string) bool {
	copyLabels := make([]string, 0, len(labels))
	previousCount := 0
	for _, label := range labels {
		if label == sourceValidationPreviousZero || label == sourceValidationPreviousOne {
			previousCount++
			continue
		}
		copyLabels = append(copyLabels, label)
	}
	if previousCount != 1 {
		return false
	}
	copyExpected := append([]string(nil), expectedRuleLabels...)
	slices.Sort(copyLabels)
	slices.Sort(copyExpected)
	return slices.Equal(copyLabels, copyExpected)
}

func sourceValidationOwnership(table *nftables.Table) (*bool, error) {
	labels, _, err := tableRuleLabels(table)
	if err != nil {
		return nil, err
	}
	var result *bool
	for _, label := range labels {
		var value bool
		switch label {
		case sourceValidationPreviousZero:
			value = false
		case sourceValidationPreviousOne:
			value = true
		default:
			continue
		}
		if result != nil {
			return nil, ErrConflict
		}
		result = new(bool)
		*result = value
	}
	return result, nil
}

func tableRuleLabels(table *nftables.Table) ([]string, map[string]Counter, error) {
	conn := &nftables.Conn{}
	chains, err := conn.ListChains()
	if err != nil {
		return nil, nil, fmt.Errorf("list nftables chains: %w", err)
	}
	labels := make([]string, 0, len(expectedRuleLabels))
	counters := make(map[string]Counter)
	preroutingFound, outputFound := false, false
	for _, chain := range chains {
		if chain.Table == nil || chain.Table.Name != table.Name || chain.Table.Family != table.Family {
			continue
		}
		switch chain.Name {
		case preroutingChainName:
			preroutingFound = true
			if !validPreroutingChain(chain) {
				labels = append(labels, "invalid-prerouting-chain")
			}
		case outputChainName:
			outputFound = true
			if !validOutputChain(chain) {
				labels = append(labels, "invalid-output-chain")
			}
		default:
			labels = append(labels, "unexpected-chain:"+chain.Name)
			continue
		}
		rules, err := conn.GetRules(table, chain)
		if err != nil {
			return nil, nil, fmt.Errorf("read Irvio protection rules: %w", err)
		}
		for _, rule := range rules {
			label, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
			if !ok {
				label = ""
			}
			labels = append(labels, label)
			for _, expression := range rule.Exprs {
				if counter, ok := expression.(*expr.Counter); ok && label != "" {
					counters[label] = Counter{Packets: counter.Packets, Bytes: counter.Bytes}
				}
			}
		}
	}
	if !preroutingFound || !outputFound {
		if !preroutingFound {
			labels = append(labels, "missing-prerouting-chain")
		}
		if !outputFound {
			labels = append(labels, "missing-output-chain")
		}
	}
	if !preroutingFound && !outputFound {
		return nil, counters, nil
	}
	return labels, counters, nil
}

func validPreroutingChain(chain *nftables.Chain) bool {
	return chain != nil && chain.Name == preroutingChainName && chain.Type == nftables.ChainTypeFilter &&
		chain.Hooknum != nil && *chain.Hooknum == *nftables.ChainHookPrerouting &&
		chain.Priority != nil && *chain.Priority == *nftables.ChainPriorityMangle &&
		chain.Policy != nil && *chain.Policy == nftables.ChainPolicyAccept
}

func validOutputChain(chain *nftables.Chain) bool {
	return chain != nil && chain.Name == outputChainName && chain.Type == nftables.ChainTypeFilter &&
		chain.Hooknum != nil && *chain.Hooknum == *nftables.ChainHookOutput &&
		chain.Priority != nil && *chain.Priority == *nftables.ChainPriorityFilter &&
		chain.Policy != nil && *chain.Policy == nftables.ChainPolicyDrop
}

func buildPreroutingRules(policy Policy, table *nftables.Table, chain *nftables.Chain) []*nftables.Rule {
	add := func(label string, mark uint32) *nftables.Rule {
		rule := &nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{
			&expr.Counter{},
			&expr.Socket{Key: expr.SocketKeyMark, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
			&expr.Immediate{Register: 1, Data: binaryutil.NativeEndian.PutUint32(mark)},
			&expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1},
		}}
		rule.UserData = userdata.AppendString(nil, userdata.TypeComment, label)
		return rule
	}
	return []*nftables.Rule{
		add("mark-inbound-bypass", policy.BypassMark),
		add("mark-inbound-tunnel-control", policy.TunnelControlMark),
	}
}

func buildRules(policy Policy, table *nftables.Table, chain *nftables.Chain, sets safetypolicy.LinuxPortSets, sourceValidationPrevious bool) []*nftables.Rule {
	add := func(label string, expressions ...expr.Any) *nftables.Rule {
		expressions = append([]expr.Any{&expr.Counter{}}, expressions...)
		rule := &nftables.Rule{Table: table, Chain: chain, Exprs: expressions}
		rule.UserData = userdata.AppendString(nil, userdata.TypeComment, label)
		return rule
	}
	rules := []*nftables.Rule{
		add(OwnerMarker),
		add(sourceValidationLabel(sourceValidationPrevious)),
		add("accept-bypass", &expr.Meta{Key: expr.MetaKeyMARK, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(policy.BypassMark)}, &expr.Verdict{Kind: expr.VerdictAccept}),
		add("accept-loopback", append(ifaceMatch(expr.MetaKeyOIFNAME, "lo"), &expr.Verdict{Kind: expr.VerdictAccept})...),
		add("accept-dhcp4", append(udpPorts(unix.NFPROTO_IPV4, 68, 67), &expr.Verdict{Kind: expr.VerdictAccept})...),
		add("accept-dhcp6", append(udpPorts(unix.NFPROTO_IPV6, 546, 547), &expr.Verdict{Kind: expr.VerdictAccept})...),
	}
	for _, item := range []struct {
		label string
		kind  byte
	}{{"accept-icmpv6-rs", 133}, {"accept-icmpv6-ra", 134}, {"accept-icmpv6-ns", 135}, {"accept-icmpv6-na", 136}} {
		rules = append(rules, add(item.label, append(icmpv6Type(item.kind), &expr.Verdict{Kind: expr.VerdictAccept})...))
	}
	tcp := append(ifaceMatch(expr.MetaKeyOIFNAME, policy.TUNName), safetypolicy.LinuxDestinationPortMatch(safetypolicy.TransportTCP, sets)...)
	tcp = append(tcp, &expr.Reject{Type: unix.NFT_REJECT_TCP_RST})
	udp := append(ifaceMatch(expr.MetaKeyOIFNAME, policy.TUNName), safetypolicy.LinuxDestinationPortMatch(safetypolicy.TransportUDP, sets)...)
	udp = append(udp, &expr.Reject{Type: unix.NFT_REJECT_ICMPX_UNREACH, Code: unix.NFT_REJECT_ICMPX_ADMIN_PROHIBITED})
	rules = append(rules,
		add("reject-safety-v0-tcp", tcp...),
		add("reject-safety-v0-udp", udp...),
	)
	// Count user OUTPUT without verdicts, excluding control packets and Exit FORWARD traffic.
	account := ifaceMatch(expr.MetaKeyOIFNAME, policy.TUNName)
	account = append(account, &expr.Meta{Key: expr.MetaKeyMARK, Register: 1},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(policy.TunnelControlMark)}, trafficstats.Reference())
	rules = append(rules, add("account-user-upload", account...),
		add("accept-tunnel", append(ifaceMatch(expr.MetaKeyOIFNAME, policy.TUNName), &expr.Verdict{Kind: expr.VerdictAccept})...))
	rules = append(rules, add("drop-ordinary", &expr.Verdict{Kind: expr.VerdictDrop}))
	return rules
}

func ifaceMatch(key expr.MetaKey, name string) []expr.Any {
	data := make([]byte, unix.IFNAMSIZ)
	copy(data, name)
	return []expr.Any{&expr.Meta{Key: key, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: data}}
}

func udpPorts(family byte, source, destination uint16) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{family}},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_UDP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(source)},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(destination)},
	}
}

func icmpv6Type(kind byte) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.NFPROTO_IPV6}},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_ICMPV6}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{kind}},
	}
}
