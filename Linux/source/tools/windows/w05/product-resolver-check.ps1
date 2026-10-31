# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Binary,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use an absolute lab path without shell metacharacters'}
if(Test-Path $evidence){throw 'Evidence directory exists'}
New-Item -ItemType Directory $evidence | Out-Null
$exe=Join-Path $Evidence 'productruntime.test.exe'
Copy-Item $Binary $exe
Get-FileHash $exe | ConvertTo-Json | Set-Content "$evidence\binary.json"
$beforeRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)"} | Sort-Object)
$beforeDNS=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
$oracle=Find-NetRoute -RemoteIPAddress 1.1.1.1
$source=@($oracle | Where-Object {$_.IPAddress})[0]
if(!$source){throw 'No native route oracle'}
$env:IRVIO_WINDOWS_PRODUCT_HOST_TEST='1'
$env:IRVIO_WINDOWS_PRODUCT_SOURCE=$source.IPAddress
$env:IRVIO_WINDOWS_PRODUCT_INTERFACE=$source.InterfaceAlias
$oracle | Format-List | Out-String | Set-Content "$evidence\oracle.txt"
& cmd.exe /d /c "$exe -test.v -test.shuffle=on -test.count=5 -test.timeout=3m > $evidence\tests.log 2>&1"
$result=$LASTEXITCODE
Get-Content "$evidence\tests.log" -Tail 50
$afterRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)"} | Sort-Object)
$afterDNS=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
$state=[pscustomobject]@{route_delta=@(Compare-Object $beforeRoutes $afterRoutes);dns_delta=@(Compare-Object $beforeDNS $afterDNS)}
$state | ConvertTo-Json -Depth 5 | Set-Content "$evidence\host-state.json"
if($state.route_delta.Count -or $state.dns_delta.Count){throw 'Host state changed'}
if($result){throw 'Native product resolver tests failed'}
if(([regex]::Matches((Get-Content -Raw "$evidence\tests.log"),'(?m)^--- PASS: TestWindowsNativeProductHostResolution \(')).Count -ne 5){throw 'Native gate skipped'}
Write-Output 'Windows product resolver native gates PASS; route/DNS baseline unchanged'
