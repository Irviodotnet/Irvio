# W00 Windows feasibility probes

Lab-only tools, not a Windows IRVIO implementation. Run only in the disposable
Windows VM with administrator access and a saved cold snapshot. No probe changes
the workstation network or proxy. The WFP oracle is a TCP listener on the separate
Linux mini-PC, `192.168.23.2:49494`; it must be reachable before testing.

## Build and run

- Extract the source tree to `C:\src\irvio` and copy this directory to
  `C:\IRVIO-Lab\windows\w00`.
- Install the project's pinned Go toolchain in `C:\Tools\go`, Git for Windows,
  and Visual Studio 2022 Community with the C++ desktop workload.
- Run `build.ps1`. The full daemon compiler inventory is saved separately and
  may fail; a successful probe build does not imply the daemon builds.
- Obtain Wintun 0.14.1 from its official distribution. Verify archive SHA-256
  `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51` and put the
  signed amd64 `wintun.dll` in `C:\IRVIO-Lab\W00` alongside the probe executable.
- Run `run-wintun.ps1` to test bidirectional IPv4 UDP packet I/O and removal of
  the owned adapter/firewall rule. NAT creation is only a diagnostic and does
  not prove forwarding.
- Run `build-wfp.ps1`, then `run-wfp.ps1`, to measure dynamic/static/persistent
  filter lifetime after forced process termination.
- For the separate persistent-filter reboot check, run
  `wfp-reboot.ps1 -Stage Prepare`, reboot the disposable VM, then run
  `wfp-reboot.ps1 -Stage Verify`. The verification removes the owned filter.
- With a UDP echo oracle on `192.168.23.2:49495` and WinNAT available, run
  `run-nat.ps1`. It requires an empty NAT inventory and tests five synthetic
  inner IPv4 sources through native forwarding/NAT, then restores the physical
  interface forwarding setting. This is not encrypted AWG/TunnelLease testing.

Results are written under `C:\IRVIO-Lab\W00`. Preserve each run's evidence
before repeating these scripts because their output filenames are fixed.

The WFP test blocks only new IPv4 TCP connections to the lab oracle. It does not
prove existing-flow protection, UDP, IPv6, BFE restart, reboot, or a complete
kill switch. Cleanup removes only the fixed W00 filter GUID. Persistent filters
can survive a crash: run `wfp-lifetime.exe remove` to remove that owned filter.

The Wintun probe uses a fixed, isolated `10.253.240.0/24` lab prefix and MTU 1280;
do not use it on a host where that prefix is already in use. The MTU is the
existing IRVIO default, not a new product setting.
