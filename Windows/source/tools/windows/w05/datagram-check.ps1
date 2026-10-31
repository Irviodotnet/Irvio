# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Binary,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use an absolute lab path without shell metacharacters'}
if(Test-Path $Evidence){throw 'Evidence exists'}
if(Get-Service IRVIO-W05-Owner -ErrorAction SilentlyContinue){throw 'Existing WFP fixture'}
$name='IrvioDatagramProbe'
if(Get-LocalUser -Name $name -ErrorAction SilentlyContinue){throw 'Existing test identity'}
$routes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
$dns=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
$adapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
New-Item -ItemType Directory $Evidence | Out-Null
$exe=Join-Path $Evidence 'owner.test.exe'
Copy-Item $Binary $exe
Copy-Item $Binary (Join-Path $Evidence 'foreign-datagram.test.exe')
Get-FileHash $exe | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binary.json')
& $exe '-test.run=^$'
if($LASTEXITCODE){throw 'Owner helper executable preflight failed'}
& (Join-Path $Evidence 'foreign-datagram.test.exe') '-test.run=^$'
if($LASTEXITCODE){throw 'Foreign helper executable preflight failed'}
$bytes=[byte[]]::new(24)
$rng=[Security.Cryptography.RandomNumberGenerator]::Create();$rng.GetBytes($bytes);$rng.Dispose()
$plain='aA1!'+[Convert]::ToBase64String($bytes)
$secret=ConvertTo-SecureString $plain -AsPlainText -Force
$createdUser=$false;$createdService=$false
try {
 $user=New-LocalUser -Name $name -Password $secret -Description 'Temporary IRVIO UDP ownership gate' -AccountNeverExpires
 $createdUser=$true
 Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545') -Member $user
 $env:IRVIO_WFP_PROBE_USER=$name;$env:IRVIO_WFP_PROBE_PASSWORD=$plain
 New-Service -Name IRVIO-W05-Owner -BinaryPathName 'C:\IRVIO-Lab\W05\filterprobe.exe service' -StartupType Automatic | Out-Null
 $createdService=$true
 Start-Service IRVIO-W05-Owner
 $env:IRVIO_WINDOWS_DATAGRAM_TEST='1'
 & cmd.exe /d /c "$exe -test.run=^TestWindowsNativeDatagramSockets$ -test.v -test.count=3 -test.timeout=3m > $Evidence\native.log 2>&1"
 $result=$LASTEXITCODE
 Get-Content (Join-Path $Evidence 'native.log') -Tail 35
 if($result){throw 'Datagram native gate failed'}
 if(([regex]::Matches((Get-Content -Raw (Join-Path $Evidence 'native.log')),'--- PASS: TestWindowsNativeDatagramSockets')).Count -ne 3){throw 'Native gate skipped'}
} finally {
 Remove-Item Env:IRVIO_WINDOWS_DATAGRAM_TEST,Env:IRVIO_WFP_PROBE_USER,Env:IRVIO_WFP_PROBE_PASSWORD -ErrorAction SilentlyContinue
 $secret.Dispose();$plain=$null
 $env:IRVIO_WINDOWS_PROTECTION_CLEANUP='1'
 & $exe '-test.run=^TestWindowsNativeProtectionCleanup$' '-test.v'
 $cleanup=$LASTEXITCODE
 Remove-Item Env:IRVIO_WINDOWS_PROTECTION_CLEANUP
 if($cleanup){throw 'Exact-owner cleanup failed; keep recovery service'}
 if($createdService){Stop-Service IRVIO-W05-Owner;& sc.exe delete IRVIO-W05-Owner;if($LASTEXITCODE){throw 'Service deletion failed'}}
 if($createdUser){Remove-LocalUser -Name $name}
 $afterRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
 $afterDNS=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
 $afterAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
 $state=[pscustomobject]@{route_delta=@(Compare-Object $routes $afterRoutes);dns_delta=@(Compare-Object $dns $afterDNS);adapter_delta=@(Compare-Object $adapters $afterAdapters)}
 $state | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'host-cleanup.json')
 if($state.route_delta.Count -or $state.dns_delta.Count -or $state.adapter_delta.Count){throw 'Host residue'}
}
Write-Output 'Native datagram ownership and host cleanup PASS'
