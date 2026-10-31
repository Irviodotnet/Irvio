# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Binary,[Parameter(Mandatory=$true)][string]$Evidence,[ValidateRange(1,10)][int]$Count=3)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use an absolute lab path without shell metacharacters'}
if(Test-Path $Evidence){throw 'Evidence exists'}
if(Get-Service IRVIO-W05-Owner -ErrorAction SilentlyContinue){throw 'Existing WFP fixture'}
if(Get-NetFirewallRule -Name IRVIO.Incoming.Lab -ErrorAction SilentlyContinue){throw 'Existing firewall fixture'}
$name='IrvioIncomingProbe'
if(Get-LocalUser -Name $name -ErrorAction SilentlyContinue){throw 'Existing test identity'}
$routes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
$dns=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
$adapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
New-Item -ItemType Directory $Evidence | Out-Null
$exe=Join-Path $Evidence 'owner.test.exe'
Copy-Item $Binary $exe
Copy-Item $Binary (Join-Path $Evidence 'foreign-incoming.test.exe')
Copy-Item 'C:\IRVIO-Lab\W06\wintun.dll' (Join-Path $Evidence 'wintun.dll')
Get-FileHash $exe | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binary.json')
$bytes=[byte[]]::new(24)
$rng=[Security.Cryptography.RandomNumberGenerator]::Create();$rng.GetBytes($bytes);$rng.Dispose()
$plain='aA1!'+[Convert]::ToBase64String($bytes)
$secret=ConvertTo-SecureString $plain -AsPlainText -Force
$createdUser=$false;$createdService=$false;$createdRule=$false
try {
 $user=New-LocalUser -Name $name -Password $secret -Description 'Temporary IRVIO incoming-control gate' -AccountNeverExpires
 $createdUser=$true
 Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545') -Member $user
 $env:IRVIO_WFP_PROBE_USER=$name;$env:IRVIO_WFP_PROBE_PASSWORD=$plain
 New-Service -Name IRVIO-W05-Owner -BinaryPathName 'C:\IRVIO-Lab\W05\filterprobe.exe service' -StartupType Automatic | Out-Null
 $createdService=$true
 Start-Service IRVIO-W05-Owner
 New-NetFirewallRule -Name IRVIO.Incoming.Lab -DisplayName 'IRVIO disposable incoming control oracle' -Direction Inbound -Action Allow -Protocol TCP -LocalPort 49498 -Profile Any | Out-Null
 $createdRule=$true
 $env:IRVIO_WINDOWS_INCOMING_TEST='1'
 & cmd.exe /d /c "$exe -test.run=^TestWindowsNativeControlListener$ -test.v -test.count=$Count -test.timeout=3m > $Evidence\native.log 2>&1"
 $result=$LASTEXITCODE
 Get-Content (Join-Path $Evidence 'native.log') -Tail 45
 if($result){throw 'Incoming native gate failed'}
 $log=Get-Content -Raw (Join-Path $Evidence 'native.log')
 if(([regex]::Matches($log,'--- PASS: TestWindowsNativeControlListener')).Count -ne $Count){throw 'Native gate skipped'}
} finally {
 Remove-Item Env:IRVIO_WINDOWS_INCOMING_TEST,Env:IRVIO_WFP_PROBE_USER,Env:IRVIO_WFP_PROBE_PASSWORD -ErrorAction SilentlyContinue
 $secret.Dispose();$plain=$null
 $env:IRVIO_WINDOWS_PROTECTION_CLEANUP='1'
 & $exe '-test.run=^TestWindowsNativeProtectionCleanup$' '-test.v'
 $cleanup=$LASTEXITCODE
 Remove-Item Env:IRVIO_WINDOWS_PROTECTION_CLEANUP
 if($cleanup){throw 'Exact-owner cleanup failed; keep recovery service'}
 if($createdRule){Remove-NetFirewallRule -Name IRVIO.Incoming.Lab}
 if($createdService){Stop-Service IRVIO-W05-Owner;& sc.exe delete IRVIO-W05-Owner;if($LASTEXITCODE){throw 'Service deletion failed'}}
 if($createdUser){Remove-LocalUser -Name $name}
 $afterRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
 $afterDNS=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
 $afterAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
 $state=[pscustomobject]@{route_delta=@(Compare-Object $routes $afterRoutes);dns_delta=@(Compare-Object $dns $afterDNS);adapter_delta=@(Compare-Object $adapters $afterAdapters);fixture_rules=@(Get-NetFirewallRule -Name IRVIO.Incoming.Lab -ErrorAction SilentlyContinue).Count}
 $state | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'host-cleanup.json')
 if($state.route_delta.Count -or $state.dns_delta.Count -or $state.adapter_delta.Count -or $state.fixture_rules){throw 'Host residue'}
}
Write-Output 'Scoped incoming listener gate and host cleanup PASS'
