# W02 Windows private profile checks

Run in the disposable Windows VM with the pinned Go toolchain, not on the Linux
workstation. These packages exercise real NTFS handles and ACLs, not a VPN:

```powershell
go vet ./internal/winfile ./internal/productprofile ./internal/identity ./internal/protocol
go test -v -shuffle=on -count=10 ./internal/winfile ./internal/productprofile ./internal/identity ./internal/protocol
go test -gcflags=all=-d=checkptr=2 ./internal/winfile ./internal/productprofile ./internal/identity ./internal/protocol
```

Repeat three shuffled iterations as SYSTEM through the independent guest agent.
Set the service-context GOMODCACHE and GOCACHE explicitly; they are not inherited
from the SSH user. Test directories are owned temporary fixtures.

`productprofile` uses native volume/File ID, not pathname equality, to reject a
replaced directory. Enumeration uses the pinned directory handle; deletion opens
all recognized regular files exclusively and validates them before any removal.
An occupied database, unknown file, permissive ACL, reparse point, hardlink or
read-only file prevents deletion. The exact existing marker bytes are preserved
and the marker is removed last. The directory and its ACL survive successful
reset. No recursive deletion or ACL repair is performed.

Directory enumeration is limited to 4096 entries. Parent directories must exist;
the helper creates only the final private directory. The Windows installer must
provision the protected parent layout and each persistent writer must use safe
Windows storage. Linux legacy profiles and their existing tests are unchanged.

The profile lifecycle test performs a refused deletion with an open database,
then a successful deletion, 100 times after warm-up. It checks file preservation
and per-batch handles, with GC/scheduling controlled and a four-handle runtime
tolerance. This is not full daemon/session lifecycle or power-loss durability.

For an actual non-admin ACL test, build `./internal/winfile` with `go test -c`.
Run `TestServiceACLFixture` as SYSTEM with `IRVIO_WINFILE_ACL_FIXTURE` pointing to
a new lab directory. It creates a file containing only `non-secret ACL fixture`.
Create a temporary non-admin local account, then run `TestUnprivilegedServiceACL`
with `IRVIO_ACL_PROBE_USER`, `IRVIO_ACL_PROBE_PASSWORD`, and `IRVIO_ACL_PROBE_PATH`
set only in that process. The test logs on, verifies the effective token, and
attempts native read/write/delete access. All three must return access denied.
Remove the temporary account and clear credential environment variables in a
finally block; never persist or commit the generated password.

Named-pipe authorization has its own `localapi-check.ps1` harness; complete store
migration, installer permissions and real UI Delete Profile remain separate gates.

## AWG transport identity

Run `awgidentity-check.ps1 -Source <source> -Evidence <new-directory>` as SYSTEM.
It executes native vet, checkptr/shuffle repetitions, a disposable non-admin ACL
probe and the broad compilable internal suite. Linux whole-tree and affected race
tests run separately on the mini-PC, not on the workstation.

The Windows backend uses the same 48-byte key/epoch representation as Linux,
but private handle-relative reads and flushed atomic writes. An operation-scoped
`awg-identity.lock` is released by closing its handle, including process death.
Competing writers fail explicitly without changing durable identity. Rotation
compares the loaded key and previous epoch under lock before replacing anything.
Delete Profile recognizes this exact Windows lock filename; it still refuses
unknown files and occupied handles.

Native tests cover persist/reload/rotate, competing creators, stale rotation,
real child-process death while locked, unsafe ACLs and links, malformed records,
occupied/read-only targets, Unicode paths and 100 rotate/reload cycles. The
ordinary-user probe checks read/write/delete denial both before and after key
rotation. No generated private keys or temporary account password belong in source/evidence.
The installer must provision protected parent directories; only the final
profile directory is created here. Power-loss durability and full daemon crash
adoption are not proved by killing the lock-holder test process.

## Windows session intent

Run `intent-check.ps1 -Source <source> -Evidence <new-directory>` as SYSTEM.
It runs internal vet, ten checkptr/shuffle repetitions and the broad internal
suite. For the real-boot fixture, use that same evidence directory:

1. Run with `-Phase seed`, then `-Phase same` in a separate process.
2. Reboot the Windows lab VM, not the Linux workstation.
3. Run with `-Phase new`; old intent must be rejected before explicit fixture cleanup.

Optionally insert a real Hibernate/resume before step 2 and run `-Phase resume`;
the same intent must remain readable. Confirm the actual power transition using
Windows boot/power events instead of trusting that `shutdown /h` or `/hybrid`
necessarily produced the requested startup mode.

Each phase refuses to overwrite its log. Boot fixture files contain only a
synthetic country intent and OS process metadata, not keys or invitations.
Tests cover actual child-process kill after CONNECT and teardown markers,
Global/private intent, unsafe ACLs/links, busy and read-only files, malformed or
oversized state, previous-boot refusal and 100 complete store cycles per repeat.
The native SCM identity test rejects a same-name decoy executable and measures
100 lookups after warm-up, allowing eight incidental Go runtime handles.

The broad gate exposed a Windows identity publication race. The harness also
runs the identity suite and 100 concurrent-creation repetitions. Windows key
reads now briefly retry only `ERROR_SHARING_VIOLATION` while an atomic publisher
closes its DELETE handle; ACL/link checks and immutable key publication remain
unchanged. A permanently occupied key still fails within a bounded interval.

Windows stores the unchanged logical State in a versioned, private envelope.
The fence is the verified LocalSystem/session-0 service-manager instance's PID
and exact kernel-recorded creation FILETIME, not `now - uptime`. No wall-clock
tolerance, shell command, boot-loader GUID or environment path is used by the
product backend. Missing/ambiguous native identity fails explicitly.

The caller must serialize lifecycle intents and perform owned cleanup before
`Clear`; the file lock does not replace the controller's request/generation
fences. `ErrPreviousBoot` never returns a resumable State or silently deletes
protection. Full SCM restart/recovery, real VPN traffic, boot cleanup and power
transitions remain separate gates. See the Windows intent ADR for limitations.

## Bootstrap persistence

Run native vet, ten shuffled repetitions and checkptr=2 for
`./internal/productbootstrap`, then three shuffled repetitions as SYSTEM.
The existing manager/protobuf implementation is shared; Windows replaces only
the POSIX file operations with the protected native storage backend.

Tests cover missing/corrupt/oversized files, private atomic replacement, busy
file preservation and temporary cleanup, public/inherited ACL rejection,
symlink/hardlink/reparse refusal, read-only targets, and Unicode directories.
Idle import does not join the network; hot import authenticates a real local
introducer. Six peers with both IPv4 and IPv6 endpoints survive merged imports
and restart without collapsing to the first address.

The 100-cycle test combines actual manager import with owned profile deletion.
It warms native APIs and controls GC/scheduling, allowing four incidental runtime
handles. Per-batch stability does not establish process-wide zero growth or full
VPN lifecycle stability. Power-loss atomicity and real UI reset still need their
own tests; parent directories must be provisioned by the installer.

## Private VPN state and admission

Run native vet and ten shuffled repetitions for `./internal/privateaccess`,
`./internal/sessioncontrol` and `./internal/tunnellease`, plus checkptr=2 for
privateaccess. Repeat privateaccess three times as SYSTEM. Run the corresponding
Linux race suites and related Rendezvous regressions on the mini-PC.

Private state keeps its existing JSON schema, identity binding, revision fences,
invitation handling and enrollment TLS. Windows owns a pinned private directory
and a native exclusive lock for the store lifetime; Close releases both before
profile deletion. A failed write poisons the store rather than continuing to
grant access from potentially ambiguous state.

The common suites cover single-use races, approvals/revocation, persisted
targets/enrollments, cancellation fences, TLS identity binding and in-flight
TunnelLease activation/renewal revocation. Windows-specific tests reject unsafe
state/lock paths and ACLs, refuse concurrent writers, and kill a real child
process holding the store before reopening its acknowledged state. The child
test is entered only by its parent test using temporary, non-secret identity/
directory arguments; it is not a daemon crash-adoption test.

One hundred cycles create/mutate the store, refuse deletion while it is open,
close it and delete its owned profile. Handle counting uses the same bounded
warm-up/GC policy as the other storage checks. Real UI, SCM adoption, WFP policy,
Windows tunnel-only direct control and mixed-OS Private VPN traffic are separate
acceptance gates, not implied by these loopback TLS and storage tests.
