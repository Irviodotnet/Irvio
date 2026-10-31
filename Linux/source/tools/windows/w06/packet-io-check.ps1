# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [Parameter(Mandatory=$true)][string]$VendorDirectory,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$SourceIPv4,
 [Parameter(Mandatory=$true)][string]$Oracle,
 [switch]$ReuseLabPayload
)
$ErrorActionPreference='Stop'
$env:IRVIO_DIVERT_IO_SOURCE=$SourceIPv4
$env:IRVIO_DIVERT_IO_ORACLE=$Oracle
& $PSScriptRoot\module-check.ps1 -TestBinary $TestBinary -VendorDirectory $VendorDirectory -Evidence $Evidence -ReuseLabPayload:$ReuseLabPayload
$log=Join-Path $Evidence 'running.log'
$probes=@(Select-String -LiteralPath $log -Pattern 'physical UDP probe=')
$traffic=@(Select-String -LiteralPath $log -Pattern '^--- PASS: TestNativeForwardAndExactReplyCapture')
$lifecycle=@(Select-String -LiteralPath $log -Pattern '^--- PASS: TestNativeOverlappedCancellationAndClose')
if($probes.Count -ne 15 -or $traffic.Count -ne 3 -or $lifecycle.Count -ne 3){throw 'Native I/O gates skipped or incomplete'}
[pscustomobject]@{physical_udp_probes=$probes.Count;native_capture_runs=$traffic.Count;lifecycle_runs=$lifecycle.Count;vpn_or_nat_integration=$false} | ConvertTo-Json | Set-Content (Join-Path $Evidence 'packet-io-result.json')
Write-Output 'Native packet I/O component PASS; full NAT/VPN integration is not exercised'
