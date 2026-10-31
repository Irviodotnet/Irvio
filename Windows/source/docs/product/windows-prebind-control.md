# Windows pre-BIND control path

This is a Windows backend foundation and staged ClientDataPlane implementation,
not a completed Windows product runtime.
The existing M12 provisional lease and authenticated session-control BIND remain
the authority for activating user traffic. No wire message or deadline changed.

`InstallRequest.TunnelControlEndpoint` carries the same typed address and port
used by `BeforeUserRoutes`; it comes from the validated session-establishment
reply. It must not be reconstructed using a fixed default control port.

Before BIND, privileged orchestration can install:

- `WindowsNetwork.TunnelControl`: at most two exact TCP endpoints on one TUN,
  constrained by interface LUID, local lease address, remote host and port.
- ALE authorization for the daemon executable and SYSTEM/Administrator, plus
  matching outbound transport rules. No ordinary tunnel permit is installed.
- `TunnelRoutes.EnsureControl`: at most two /32 or /128 host routes on the
  exclusively owned Wintun. These are not default routes or management bypasses.

Underlay permissions cannot use that control TUN. Pre-BIND endpoints cannot be
combined with `TunnelLUID` full-user permission. Network updates remain atomic
and installation-epoch fenced. Removing protection resets permissions to the
closed baseline; a later Connect cannot resurrect the previous endpoint.

Owned routes use journal version 1 for existing split-default routes and version
2 for control-only host routes. Version 2 rejects subnet/default, unspecified,
loopback, multicast and IPv4-mapped addresses. Empty clean state remains version
1. Both versions retain the existing private ACL, exclusive journal lock,
GUID ownership, exact native-row checks and partial-rollback bookkeeping.
An older reader refuses version 2 rather than guessing ownership. Switching
between control and user route sets requires explicit removal; no implicit
promotion occurs inside the route manager.

The shared ClientDataPlane retains Linux generation/token fencing, promotion,
AWG key rotation and logical TUN reuse. Its Windows platform adapter composes
AWG handshake → exact control permission/routes → `BeforeUserRoutes` BIND
callback → split routes → lease-family DNS → full tunnel permission. Missing
control endpoint/callback or unsupported Linux bypass settings are rejected.
Journal parents use the existing private Windows ACL/handle ownership helper.

On failure, the adapter closes the acquired control channel, revokes tunnel
permission and restores owned DNS/routes while retaining protection. Normal
teardown invokes the existing control closer before route/DNS/AWG/TUN removal.
`UpdateTunnel` and `UpdateUnderlay` preserve the other lane under the same WFP
lock, avoiding resurrection from stale independently composed snapshots.

Production daemon composition and network-generation reconciliation remain
open. The original native installer test uses a test BIND callback and a
ChannelTUN remote AWG peer. A separate mixed-OS gate now uses real Linux TUN,
AWG, TunnelLease LifecycleAuthority and Noise session control: a wrong session
token is refused, then valid BIND, user UDP, RENEW and RELEASE succeed. It still
uses fixture-supplied keys/lease/selected endpoints, not ICE or discovery. Linux
production lifecycle/failover tests separately exercise the unchanged common
lifecycle against real daemon peers.

Native evidence currently covers emitted TCP packets on real Wintun, ordinary
user/foreign executable denial, tuple/protocol restrictions, IPv4/IPv6, revocation
and explicit user permission. The packet oracle does not complete an authenticated
TunnelLease handshake and must not be reported as a successful VPN connection.
See `docs/testing/windows-port-validation.md` for validation and remaining scope.
