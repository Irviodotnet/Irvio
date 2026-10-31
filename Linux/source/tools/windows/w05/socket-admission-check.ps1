# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Binary,[Parameter(Mandatory=$true)][string]$CleanupBinary,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use an absolute lab path without shell metacharacters'}
if(Test-Path $Evidence){throw 'Evidence exists'}
if(Get-Service IRVIO-W05-Owner -ErrorAction SilentlyContinue){throw 'Existing WFP fixture'}
if(@(Get-NetFirewallRule | Where-Object {$_.Group -eq 'IRVIO.Inbound.v1'}).Count){throw 'Existing physical firewall scope'}
$routes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
$dns=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
$adapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
New-Item -ItemType Directory $Evidence | Out-Null
$exe=Join-Path $Evidence 'runtime.test.exe'
Copy-Item $Binary $exe
Get-FileHash $exe | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binary.json')
& $exe '-test.run=^$'
if($LASTEXITCODE){throw 'Executable preflight failed'}
$createdService=$false
try {
 New-Service -Name IRVIO-W05-Owner -BinaryPathName 'C:\IRVIO-Lab\W05\filterprobe.exe service' -StartupType Automatic | Out-Null
 $createdService=$true
 Start-Service IRVIO-W05-Owner
 $env:IRVIO_WINDOWS_SOCKET_ADMISSION_TEST='1'
 & cmd.exe /d /c "$exe -test.run=^TestWindowsNativeSocketAdmission$ -test.v -test.count=3 -test.timeout=2m > $Evidence\native.log 2>&1"
 $result=$LASTEXITCODE
 Get-Content "$Evidence\native.log" -Tail 45
 if($result){throw 'Native socket admission failed'}
 if(([regex]::Matches((Get-Content -Raw "$Evidence\native.log"),'--- PASS: TestWindowsNativeSocketAdmission ')).Count -ne 3){throw 'Native gate skipped'}
} finally {
 Remove-Item Env:IRVIO_WINDOWS_SOCKET_ADMISSION_TEST -ErrorAction SilentlyContinue
 $env:IRVIO_WINDOWS_PROTECTION_CLEANUP='1'
 & $CleanupBinary '-test.run=^TestWindowsNativeProtectionCleanup$' '-test.v'
 $cleanup=$LASTEXITCODE
 Remove-Item Env:IRVIO_WINDOWS_PROTECTION_CLEANUP
 if($cleanup){throw 'Exact-owner WFP cleanup failed; keep recovery service'}
 if($createdService){Stop-Service IRVIO-W05-Owner;& sc.exe delete IRVIO-W05-Owner;if($LASTEXITCODE){throw 'Service deletion failed'}}
 $afterRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
 $afterDNS=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
 $afterAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
 $state=[pscustomobject]@{route_delta=@(Compare-Object $routes $afterRoutes);dns_delta=@(Compare-Object $dns $afterDNS);adapter_delta=@(Compare-Object $adapters $afterAdapters);rules=@(Get-NetFirewallRule | Where-Object {$_.Group -eq 'IRVIO.Inbound.v1'}).Count}
 $state | ConvertTo-Json -Depth 5 | Set-Content "$Evidence\host-cleanup.json"
 if($state.route_delta.Count -or $state.dns_delta.Count -or $state.adapter_delta.Count -or $state.rules){throw 'Host residue'}
}
Write-Output 'Production hosting/protected socket admission and cleanup PASS'
