# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
New-IRVIOServerDirectory $Evidence
$root=Join-Path $Evidence 'state';New-IRVIOServerDirectory $root
$program=Join-Path $Evidence 'program'
$bootstrap='irvio-bootstrap:v1:QUJD'
[IO.File]::WriteAllText("$root\server-bootstrap.txt",$bootstrap,[Text.Encoding]::ASCII)
Protect-UpdateFile "$root\server-bootstrap.txt"
$files=@('daemon/irviod.exe','daemon/wintun.dll','driver/WinDivert.dll','driver/WinDivert64.sys','ui/irvio.exe','ui/irvio-ui-bridge.exe','geo/country.mmdb')|ForEach-Object {@{path=$_;size=1;sha256=('a'*64)}}
Write-UpdateJSON "$root\uninstall-manifest.json" @{schema=1;product='IRVIO';platform='windows-amd64';development_unsigned=$true;files=$files}
$record=@{schema=1;role='server';manifest_sha256=(Get-FileHash "$root\uninstall-manifest.json").Hash.ToLowerInvariant();bootstrap_sha256=(Get-FileHash "$root\server-bootstrap.txt").Hash.ToLowerInvariant();development_unsigned=$true}
$receipt=@{schema=2;phase='completed';record=$record;retain_profile=$true;shortcut_sha256=''}
Write-UpdateJSON "$root\uninstalled.json" $receipt
$results=New-Object 'System.Collections.Generic.List[object]'
function Reject([string]$Name,[scriptblock]$Action,[string]$Pattern){
 $caught=$null;try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Pattern){throw "Wrong refusal for ${Name}: $caught"}
 $results.Add(@{test=$Name;result='PASS'})
}
$read={Read-IRVIOPreservedServerInstallation $root $program $bootstrap}
& $read|Out-Null
$results.Add(@{test='retained-server-receipt-accepted';result='PASS'})
Reject 'changed-bootstrap-refused' {Read-IRVIOPreservedServerInstallation $root $program 'irvio-bootstrap:v1:QUJE'} 'retained bootstrap bundle'
$receipt.phase='files-removed';Write-UpdateJSON "$root\uninstalled.json" $receipt
Reject 'incomplete-uninstall-refused' $read 'not completed'
$receipt.phase='completed';Write-UpdateJSON "$root\uninstalled.json" $receipt
[IO.File]::AppendAllText("$root\server-bootstrap.txt",'A')
Reject 'bootstrap-file-tampering-refused' $read 'bootstrap file differs'
[IO.File]::WriteAllText("$root\server-bootstrap.txt",$bootstrap,[Text.Encoding]::ASCII)
[IO.File]::WriteAllText("$root\desktop-user.sid",'S-1-5-21-101-202-303-1001')
try{Reject 'mixed-desktop-authorization-refused' $read 'Desktop authorization'}finally{[IO.File]::Delete("$root\desktop-user.sid")}
$receipt.retain_profile=$false;Write-UpdateJSON "$root\uninstalled.json" $receipt
& $read|Out-Null
$results.Add(@{test='erased-profile-accepted';result='PASS'})
New-IRVIOServerDirectory "$root\profile"
[IO.File]::WriteAllText("$root\profile\unexpected.txt",'fixture only')
try{Reject 'profile-resurrection-refused' $read 'Profile data appeared'}finally{[IO.File]::Delete("$root\profile\unexpected.txt")}
[IO.File]::AppendAllText("$root\uninstall-manifest.json",' ')
Reject 'retained-manifest-tampering-refused' $read 'manifest differs'
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'SERVER RETAINED PROFILE FIXTURES PASS; no canonical installation modified'
