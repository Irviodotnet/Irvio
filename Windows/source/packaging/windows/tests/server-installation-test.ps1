# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
if(Test-Path $Evidence){throw 'New evidence required'}
New-IRVIOServerDirectory $Evidence
$root=Join-Path $Evidence 'state';$program=Join-Path $Evidence 'program';$driver=Join-Path $Evidence 'driver'
foreach($path in @($root,$program,$driver)){New-IRVIOServerDirectory $path}
$rows=@()
foreach($path in @('daemon/irviod.exe','daemon/wintun.dll','driver/WinDivert.dll','driver/WinDivert64.sys','ui/irvio.exe','ui/irvio-ui-bridge.exe','geo/country.mmdb')){
 $parts=$path.Split('/',2)
 $content='fixture only, never executable'
 $hash=[Security.Cryptography.SHA256]::Create()
 try{$digest=[BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::ASCII.GetBytes($content))).Replace('-','').ToLowerInvariant()}finally{$hash.Dispose()}
 $rows+=@{path=$path;sha256=$digest;size=$content.Length}
 if($parts[0] -eq 'ui'){continue}
 $base=switch($parts[0]){'daemon'{"$program\daemon"};'driver'{$driver};'geo'{"$root\geo"}}
 $target=Join-Path $base $parts[1]
 New-Item -ItemType Directory ([IO.Path]::GetDirectoryName($target)) -Force|Out-Null
 [IO.File]::WriteAllText($target,$content,[Text.Encoding]::ASCII)
 Protect-UpdateFile $target
}
[IO.File]::WriteAllText("$program\payload-manifest.json",(@{schema=1;product='IRVIO';platform='windows-amd64';development_unsigned=$true;files=$rows}|ConvertTo-Json -Depth 5))
[IO.File]::WriteAllText("$root\server-bootstrap.txt",'irvio-bootstrap:v1:QUJD',[Text.Encoding]::ASCII)
Protect-UpdateFile "$root\server-bootstrap.txt"
$record=[pscustomobject]@{schema=1;role='server';manifest_sha256=(Get-FileHash "$program\payload-manifest.json").Hash.ToLowerInvariant();bootstrap_sha256=(Get-FileHash "$root\server-bootstrap.txt").Hash.ToLowerInvariant();development_unsigned=$true}
$results=New-Object 'System.Collections.Generic.List[object]'
function Reject([string]$Name,[scriptblock]$Action,[string]$Expected){
 $caught=$null;try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong rejection for ${Name}: $caught"}
 $results.Add(@{test=$Name;result='PASS'})
}
$read={Read-IRVIOInstalledImage $root $program $driver $record "$root\geo" 'server'}
$installed=& $read
if($installed.files.Count -ne 5 -or (Test-Path "$program\ui")){throw 'Server UI projection failed'}
$results.Add(@{test='headless-file-inventory';result='PASS'})
$driverFile=Join-Path $driver 'WinDivert.dll'
$acl=Get-Acl $driverFile
$acl.SetAccessRuleProtection($false,$true)
Set-Acl $driverFile $acl
try{Reject 'inherited-driver-acl' $read 'ACL is inherited'}finally{Protect-UpdateFile $driverFile}
Reject 'desktop-refuses-server-record' {Assert-IRVIOInstallationRecord $record} 'Unsupported installation record'
$command=Get-IRVIOServerCommand $installed
$service=[pscustomobject]@{Path=$command;Account='LocalSystem';Type=16}
$recovery=[pscustomobject]@{Path=('"'+$program+'\daemon\irviod.exe" --boot-recover');Account='LocalSystem';Type=16;Start=3}
$kernel=[pscustomobject]@{Path=($driver+'\WinDivert64.sys');Type=1;Start=3}
$check={Assert-IRVIOServerServices $installed $service $recovery $kernel}
& $check
$results.Add(@{test='headless-service-inventory';result='PASS'})
$service.Path+=' --country XX';Reject 'engineering-argument-drift' $check 'SCM ownership';$service.Path=$command
$service.Account='LocalService';Reject 'wrong-service-account' $check 'SCM ownership';$service.Account='LocalSystem'
$recovery.Start=2;Reject 'automatic-recovery-race' $check 'demand-start';$recovery.Start=3
$kernel.Path='C:\foreign.sys';Reject 'foreign-driver' $check 'registration is not owned';$kernel.Path=$driver+'\WinDivert64.sys'
[IO.File]::AppendAllText("$root\server-bootstrap.txt",'A');Reject 'bootstrap-tampering' $check 'bootstrap file differs'
[IO.File]::WriteAllText("$root\server-bootstrap.txt",'irvio-bootstrap:v1:QUJD',[Text.Encoding]::ASCII)
foreach($bad in @("irvio-bootstrap:v1:QUJD`n",'irvio-bootstrap:v1:QUJD --role desktop','irvio-bootstrap:v1:$(x)','hydra-bootstrap:v1:QUJD')){Reject 'bootstrap-shell-or-frame' {Assert-IRVIOServerBootstrapText $bad} 'Invalid server bootstrap'}
New-Item -ItemType Directory "$program\ui"|Out-Null
try{Reject 'mixed-desktop-files' $read 'desktop authorization or UI'}finally{[IO.Directory]::Delete("$program\ui")}
Assert-IRVIOServerPortsAvailable
$results.Add(@{test='all-server-ports-unused';result='PASS'})
$listener=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,4000)
try{$listener.Start();Reject 'busy-bootstrap-port' {Assert-IRVIOServerPortsAvailable} 'Server port already in use: 4000'}finally{$listener.Stop()}
foreach($port in @(3478,51820)){
 $udp=[Net.Sockets.UdpClient]::new()
 try{$udp.Client.Bind([Net.IPEndPoint]::new([Net.IPAddress]::Loopback,$port));Reject "busy-udp-$port" {Assert-IRVIOServerPortsAvailable} "Server port already in use: $port"}finally{$udp.Dispose()}
}
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'SERVER INSTALLATION FIXTURES PASS; no canonical install or network operation'
