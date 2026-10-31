# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [Parameter(Mandatory=$true)][string]$VendorDirectory,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(!$env:IRVIO_DP_PEER_LISTEN -or !$env:IRVIO_DP_PEER_NONCE){throw 'Linux peer fixture required'}
if(Get-Service IRVIO-W05-Owner -ErrorAction SilentlyContinue){throw 'WFP fixture already exists'}
if(Get-NetAdapter -IncludeHidden | Where-Object Name -eq 'IRVIO-DP'){throw 'TUN fixture already exists'}
if(Get-NetFirewallRule -Name 'IRVIO.Control.*' -ErrorAction SilentlyContinue){throw 'Existing control firewall rules'}
$routes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
$dns=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
$adapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
$created=$false
try {
 New-Service -Name IRVIO-W05-Owner -BinaryPathName 'C:\IRVIO-Lab\W05\filterprobe.exe service' -StartupType Automatic | Out-Null
 $created=$true
 Start-Service IRVIO-W05-Owner
 $env:IRVIO_EXIT_DP_NETWORK_BIND='1'
 & (Join-Path $PSScriptRoot 'module-check.ps1') -TestBinary $TestBinary -VendorDirectory $VendorDirectory -Evidence $Evidence -ReuseLabPayload -TestPattern '^TestWindowsNativeExitOwnNetworkBind$' -RunningCount 1 -TestTimeout 3m
 $log=Get-Content -Raw (Join-Path $Evidence 'running.log')
 if($log -notmatch '--- PASS: TestWindowsNativeExitOwnNetworkBind'){throw 'Native protected Exit BIND gate skipped/failed'}
} finally {
 Remove-Item Env:IRVIO_EXIT_DP_NETWORK_BIND -ErrorAction SilentlyContinue
 $env:IRVIO_CLIENT_DP_CLEANUP='1'
 & $TestBinary '-test.run=^TestWindowsNativeClientDPCleanup$' '-test.v'
 $cleanup=$LASTEXITCODE
 Remove-Item Env:IRVIO_CLIENT_DP_CLEANUP
 if($cleanup){throw 'WFP cleanup failed; preserve service fixture'}
 if($created){Stop-Service IRVIO-W05-Owner; & sc.exe delete IRVIO-W05-Owner; if($LASTEXITCODE){throw 'Service cleanup failed'}}
 $afterRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
 $afterDNS=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
 $afterAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
 $rules=@(Get-NetFirewallRule -Name 'IRVIO.Control.*' -ErrorAction SilentlyContinue)
 $state=[pscustomobject]@{route_delta=@(Compare-Object $routes $afterRoutes);dns_delta=@(Compare-Object $dns $afterDNS);adapter_delta=@(Compare-Object $adapters $afterAdapters);control_rules=$rules.Count}
 $state | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'host-cleanup.json')
 if($state.route_delta.Count -or $state.dns_delta.Count -or $state.adapter_delta.Count -or $rules.Count){throw 'Host residue'}
}
Write-Output 'Protected Exit and real upstream Noise BIND/RENEW/RELEASE PASS; ICE/discovery remains separate'
