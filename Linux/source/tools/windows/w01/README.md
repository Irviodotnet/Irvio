# Windows state backend validation

Disposable Windows VM only. These scripts are laboratory tooling, not installer
actions. They never use real IRVIO keys. Run Go commands from `C:\src\irvio`:

```powershell
go vet ./internal/winfile ./internal/identity ./internal/protocol
go test -shuffle=on -count=10 ./internal/winfile ./internal/identity ./internal/protocol
go test -gcflags=all=-d=checkptr=2 -v ./internal/winfile ./internal/identity ./internal/protocol
```

Repeat the tests as `SYSTEM` through the out-of-band lab agent. Use an explicit
offline module cache if that account has no populated cache. A different Windows
edition, a race run, and power-loss durability are separate gates.

## Real non-admin access check

1. Build the test helper into the existing lab directory:

   ```powershell
   go test -c -o C:\IRVIO-Lab\W01\winfile.test.exe ./internal/winfile
   ```

2. Run `create-acl-fixture.ps1` as SYSTEM. It refuses an existing fixture path and
   creates `C:\IRVIO-Lab\W01-service-acl\state` using the actual private-state
   implementation.
3. Run `check-user-acl.ps1` from an elevated non-SYSTEM administrator shell. It
   creates a temporary standard account with a random in-memory password, uses
   its real logon/impersonation token, verifies direct Windows read/write access
   is denied, and removes the account in `finally`. No password is logged.
4. Save `C:\IRVIO-Lab\W00\w01-unprivileged-acl.log` and the fixture ACL, then
   remove exactly the generated `state` file and empty `W01-service-acl`
   directory. Do not recursively remove the lab or other profile directories.

Both environment-gated fixture/token helpers do nothing in ordinary `go test`
runs. Their absence of execution is not an ACL integration PASS. The explicit
script invocation and corresponding result log are required evidence.

## Shared runtime / native control transport

From a fresh source extraction in the disposable Windows VM, with the pinned Go
toolchain and offline module cache configured:

```powershell
.\tools\windows\w01\runtime-check.ps1 -Evidence C:\IRVIO-Lab\evidence\runtime-new
```

The evidence directory must not exist. This runs native vet and ten shuffled
checkptr-enabled repetitions of `runtime`, `socketmux`, and `transportgen`.
The runtime tests use real loopback libp2p nodes, native interface-bound TCP,
M11 quorum, readiness-driven public/private Rendezvous services and private
registration/lookup. They reject missing policy and failed request/challenge
bindings without using existing connections as a fallback. The native socket
tests retain exclusive UDP ownership and check fixed-port generation rebind.
The shared promotion cleanup test counts Windows handles instead of Linux FDs.

These are common-runtime and native-socket tests, not a Windows daemon, external
reachability proof, full AWG data plane, or mixed-OS VPN acceptance. Test identities
and state use temporary private directories; no installed profile is used.
Linux regression must independently run on the Linux lab, including
`go vet ./internal/runtime ./cmd/irviod ./internal/socketmux ./internal/transportgen`
and the same packages with `go test -race -shuffle=on -count=10`.
