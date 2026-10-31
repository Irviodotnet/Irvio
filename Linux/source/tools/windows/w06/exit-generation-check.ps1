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
 [ValidateRange(1,20)][int]$Count=3
)
$ErrorActionPreference='Stop'
if(Test-Path $Evidence){throw 'Evidence already exists'}
$beforeAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
$beforeRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
if(Get-NetFirewallRule -Name 'IRVIO.Control.*' -ErrorAction SilentlyContinue){throw 'Existing IRVIO control rules'}
$env:IRVIO_WINDOWS_EXIT_GENERATION='1'
$env:IRVIO_WINDOWS_NAT_DNS_ORACLE=$PublicDNSOracle
try {
 & (Join-Path $PSScriptRoot 'module-check.ps1') -TestBinary $TestBinary -VendorDirectory $VendorDirectory -Evidence $evidence -ReuseLabPayload:$ReuseLabPayload -TestPattern '^TestWindowsNativeExitGeneration$' -RunningCount $Count -TestTimeout 3m
 $log=Get-Content -Raw (Join-Path $evidence 'running.log')
 if(([regex]::Matches($log,'--- PASS: TestWindowsNativeExitGeneration')).Count -ne $Count){throw 'Native generation skipped/failed'}
} finally {
 $afterAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
 $afterRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
 $rules=@(Get-NetFirewallRule -Name 'IRVIO.Control.*' -ErrorAction SilentlyContinue)
 $state=[pscustomobject]@{adapter_delta=@(Compare-Object $beforeAdapters $afterAdapters);route_delta=@(Compare-Object $beforeRoutes $afterRoutes);control_rules=$rules.Count}
 $state | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $evidence 'host-cleanup.json')
 if($state.adapter_delta.Count -or $state.route_delta.Count -or $rules.Count){throw 'Host residue'}
 Remove-Item Env:IRVIO_WINDOWS_EXIT_GENERATION -ErrorAction SilentlyContinue
 Remove-Item Env:IRVIO_WINDOWS_NAT_DNS_ORACLE -ErrorAction SilentlyContinue
}
Write-Output 'Native Windows generation composition PASS; full daemon not yet composed'
