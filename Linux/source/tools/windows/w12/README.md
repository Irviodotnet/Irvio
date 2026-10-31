# Native Windows whole-tree gates

Run `integration-check.ps1` only in a disposable elevated Windows VM, from a
frozen source tree, with native Go, CGO and a compatible C compiler configured.
Do not run it on the user's workstation or concurrently with other network
experiments in the same VM.

```powershell
.\tools\windows\w12\integration-check.ps1 -DisposableVM -Evidence C:\IRVIO-Lab\new-integration
.\tools\windows\w12\integration-check.ps1 -DisposableVM -Scope Full -Evidence C:\IRVIO-Lab\new-full
```

The migration fixtures use 127.0.0.2 and 127.0.0.3 as distinct source addresses.
Unlike Linux implicit loopback aliases, Windows source-owner validation requires
them to appear in the native unicast table. The harness assigns temporary /32
addresses to the existing loopback interface, preserves every migration
assertion, and removes only its own additions in finally. Pre-existing aliases
are a refusal, not authorization to remove them. The final address/route snapshot
must equal baseline; preserve failure evidence when it does not.

Default scope runs the integration package with race/shuffle ×10. Full scope
runs vet, normal tests, shuffle ×10, race, race/shuffle ×5, and normal/trimpath
daemon builds. All stages retain exit codes and monotonic durations. Native
privileged tests gated by explicit environment variables are still separate
acceptance gates; a whole-tree pass does not imply those tests ran.
