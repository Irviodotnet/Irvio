# Exit Egress Safety Policy v0

HYDRA Safety Policy v0 is a narrow transport-port deny policy for ordinary
user traffic carried by the HYDRA data plane. It is abuse-prevention
hardening, not a general host firewall, DPI system, or acceptable-use
protocol.

## Canonical policy

The sole machine-readable definition is `internal/safetypolicy`. Both Linux
enforcement points compile that definition into nftables constant sets.

| Transport | Blocked destination ports | Service classes |
| --- | --- | --- |
| TCP | `19`, `25`, `135`, `137-139`, `445` | CHARGEN, SMTP server-to-server, RPC endpoint mapper, NetBIOS, SMB |
| UDP | `19`, `135`, `137-139`, `1900`, `11211` | CHARGEN, RPC endpoint mapper, NetBIOS, SSDP, memcached |

The lists are intentionally asymmetric. In particular, UDP/25, UDP/445,
TCP/1900, and TCP/11211 are not blocked by v0.

## Enforcement and trust boundary

An official client installs the policy before connection discovery begins.
Rules match inner TCP or UDP traffic whose output interface is `hydra0` and
reject it locally. Disconnect removes the HYDRA-owned table, so the policy
does not block ordinary traffic through the host WAN while VPN intent is off.

An official Exit independently checks forwarded inner packets arriving from
`hydra0`. It drops blocked destination ports before the general
`hydra0`-to-WAN accept and before NAT. This Exit rule is the security trust
boundary: it remains effective when a modified client removes or bypasses its
local policy. Modified-client plus modified-Exit behavior is outside what a
decentralized system can enforce.

Both paths use an `inet` nftables table and therefore cover IPv4 and IPv6.
The rules operate in netfilter filter hooks after reassembly; regression tests
also inject out-of-order IPv4 and IPv6 fragments and independently verify that
no blocked packet leaves the Exit WAN.

## What is not filtered

The policy does not inspect DHT, libp2p, Rendezvous, STUN, ICE checks, M11
reachability, session control, or outer AWG UDP. Client rules are scoped to
inner traffic leaving through `hydra0`; Exit rules are scoped to inner traffic
forwarded from `hydra0`. An outer UDP source or destination port that happens
to equal a denied service port is therefore not interpreted as user traffic.

ICMP, ESP, GRE, payload contents, destination addresses, domains, scans, and
traffic rates are outside v0. Existing Exit anti-spoofing, local-destination
isolation, anti-multihop, TunnelLease, and ProtectedMode rules remain separate
and continue to apply.

## Failure and lifecycle semantics

Client policy installation is part of the fail-closed `Connect` transition.
Connection discovery cannot start if installation or verification fails. Exit
policy creation is atomic and Exit startup fails before serving user traffic
if the policy cannot be installed.

HYDRA owns its nftables tables exclusively. Repeated install/remove is
idempotent. A valid table left by a crash is adopted and reconciled; missing
sets, missing rules, or extra residue are repaired before use. An unowned
table with the production name is rejected rather than modified.

## Privacy and observability

Every client and Exit transport rule has an aggregate nftables packet/byte
counter. The implementation does not log source addresses, destination
addresses, payloads, or per-packet timestamps. No user-packet logging or DPI
is performed.

## Limitations

Policy v0 is built into official software and is not negotiated over the wire.
It does not claim enforcement when both endpoints are modified, and it is not
a replacement for legal policy, abuse response, or broader network security
controls.
