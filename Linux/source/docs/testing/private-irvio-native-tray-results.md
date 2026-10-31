# Private IRVIO — native AppIndicator shutdown

Mini-PC, packaged preview `bbeb819`; no production code changed. Daemon SHA-256 `a6ebc8837e286ec101ca9f74e631940cfe36884649bb17c11d4464b1054b5fd0`; UI library `824124c91a17000f9634eaf64f77dd39181cd6596eee79dd861c7926dc7e9ed0`. Runner `private-tray.test` SHA-256 `1bb74ca5d287d9ae320d899db44720d17b0763dba6cfddb65a4397a8ebbfc3da`.

## Completed proof

Artifact `/tmp/hydra-m4b-0c03e0db`: 2026-10-06 22:19:17.561554464–22:21:37.090328614 UTC. `TestPrivateRuntimeCombinedGUIShutdown` PASS in 139.48s; terminal exit 0, host integrity true. Duration includes manual inspection, not shutdown latency.

The real packaged GTK/Flutter UI ran under isolated Xvfb with a dedicated D-Bus session. The host had a pinned private upstream and one active downstream client. The exported native AppIndicator menu reported `Connected — Private Network` and contained `Shutdown IRVIO`. `com.canonical.dbusmenu.Event` on the discovered Shutdown item delivered the real plugin event, not a direct Dart/controller/API invocation.

The confirmation dialog was visually inspected, Cancel clicked using X input, the native menu action delivered again, and Shutdown confirmed using X input. The harness then required successful GUI and daemon exit, cleared combined intent, restored host network baseline and ordinary WAN, released upstream lease, blocked downstream user traffic, zero scoped WAN leaks and explicit downstream/upstream cleanup. Final inspection found only the pre-existing management namespace, no lab daemon/runner and no hydrad cgroups.

This proves native menu protocol/plugin delivery and combined shutdown, not visual panel-icon integration in GNOME/Wayland. No panel watcher was used. It also does not resolve the older rapid Cancel/reclose black-frame observation; the spaced native dialog rendered correctly in this run. Screenshots contain lab identity/status and remain private local evidence.

## Harness changes and failed attempts

`IRVIO_PRIVATE_GUI_DBUS=1` now opts the existing GUI fixture into a dedicated `dbus-daemon --session --nofork`, scoped to its namespace/runtime directory and stopped with the lab. The existing default fixture remains unchanged. Window discovery retries transient xdotool errors under the same 15-second total deadline.

- `a64d71be`, 23.07s: the first harness mistakenly used a regular-file readiness helper on a Unix socket. D-Bus stderr was empty; waiting for its nonempty `--print-address=1` output corrected the check.
- `e038ddd7`, 21.84s: xdotool failed with `BadWindow` while scanning startup windows. GUI stderr contained renderer/plugin notices, not a crash. Bounded search retry allowed the next attempt to reach the visible window.
- Both early-abort runs reported host-integrity failure from one retained cgroup (`hydrad-a22cec4ec21ff378`, then `hydrad-bb200ace6030b9b7`). Each was resolved under the current SSH session cgroup, inspected with empty `cgroup.procs`, and removed with scoped `rmdir` before the next run. No namespace/daemon remained. Failed results were not rewritten as PASS; the exact early-abort cleanup timing remains separate from successful daemon-owned shutdown.

Mini-PC runner compilation and `go vet ./test/netns/m4b` passed. No installed application, workstation runtime, production distributor or public release was changed.
