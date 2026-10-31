# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [Parameter(Mandatory=$true)][string]$VendorDirectory,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$PublicDNSOracle,
 [switch]$ReuseLabPayload,
 [ValidateRange(1,10)][int]$Count=3
)
$ErrorActionPreference='Stop'
if(Test-Path $Evidence){throw 'Evidence already exists'}
$beforeAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
$beforeRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
if(Get-NetFirewallRule -Name 'IRVIO.Control.*' -ErrorAction SilentlyContinue){throw 'Existing IRVIO control rules'}
$env:IRVIO_WINDOWS_EXIT_SERVER='1'
$env:IRVIO_WINDOWS_NAT_DNS_ORACLE=$PublicDNSOracle
try {
 & (Join-Path $PSScriptRoot 'module-check.ps1') -TestBinary $TestBinary -VendorDirectory $VendorDirectory -Evidence $Evidence -ReuseLabPayload:$ReuseLabPayload -TestPattern '^TestWindowsNativeExitServerComposition$' -RunningCount $Count -TestTimeout 3m
 $log=Get-Content -Raw (Join-Path $Evidence 'running.log')
 if(([regex]::Matches($log,'--- PASS: TestWindowsNativeExitServerComposition')).Count -ne $Count){throw 'Native server gate skipped/failed'}
} finally {
 $afterAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
 $afterRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
 $rules=@(Get-NetFirewallRule -Name 'IRVIO.Control.*' -ErrorAction SilentlyContinue)
 $state=[pscustomobject]@{adapter_delta=@(Compare-Object $beforeAdapters $afterAdapters);route_delta=@(Compare-Object $beforeRoutes $afterRoutes);control_rules=$rules.Count}
 $state | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'host-cleanup.json')
 if($state.adapter_delta.Count -or $state.route_delta.Count -or $rules.Count){throw 'Host residue'}
 Remove-Item Env:IRVIO_WINDOWS_EXIT_SERVER -ErrorAction SilentlyContinue
 Remove-Item Env:IRVIO_WINDOWS_NAT_DNS_ORACLE -ErrorAction SilentlyContinue
}
Write-Output 'Native ExitServer/lease composition PASS; discovery and protected CLIENT+EXIT remain separate gates'
