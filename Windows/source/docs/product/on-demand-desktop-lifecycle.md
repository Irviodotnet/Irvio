# HYDRA Linux on-demand desktop lifecycle

The packaged desktop uses the three unit files in `packaging/systemd/`:

- `irvio-boot-recover.service` is a boot-time oneshot. It compares a root-owned boot marker, reconciles only HYDRA-owned previous-boot protection/routing residue, and exits. It does not connect the VPN.
- `irvio.socket` owns `/run/irvio/control.sock` and remains listening after daemon shutdown. The `irvio` group controls access. The socket owns the `/run/irvio` directory lifetime; `irviod.service` must not use `RuntimeDirectory=irvio` because that would remove the socket and same-boot ConnectIntent during service restart.
- `irviod.service` is started by socket activation, not enabled as a boot service. `Restart=on-failure` restarts crashes but not a successful Shutdown exit.

Packaging must create the `irvio` group, install the daemon at `/usr/local/bin/irviod` (or consistently adjust both unit files), install the units, enable `irvio.socket` and `irvio-boot-recover.service`, and avoid enabling a competing always-on desktop service. These files are Linux host-namespace units; arbitrary container/network-namespace deployment is not supported by the Alpha DNS path.

Opening the UI connects to the Local API and starts the daemon idle. Importing bootstrap data does not enable ProtectedMode. CONNECT persists an owner-only same-boot country intent in `/run/irvio/session-state` and starts the existing CLIENT+EXIT production runtime. On failure or daemon crash, ordinary user traffic remains HYDRA-or-DROP; a systemd restart adopts protection and retries country discovery, not a particular stale Exit. A missing or malformed intent with installed protection is never interpreted as permission to use physical WAN.

DISCONNECT atomically records teardown intent before closing new Exit admissions, withdrawing registrations, revoking downstream leases, closing the own tunnel and DHT, and restoring DNS/routes/WAN. A crash during this sequence finishes teardown after restart. SHUTDOWN performs the same work, acknowledges through Local API v1.4, and exits with status 0. The UI stops Local API reconnect timers before exiting. If the UI crashes unexpectedly while connected, the daemon remains active. An idle daemon with no API clients exits after one minute; the socket remains.

At a new boot `/run` is empty. The oneshot runs before the socket becomes available. The resulting desktop state is IDLE with ordinary WAN and no auto-connect. The boot marker is not a persistent ConnectIntent.
