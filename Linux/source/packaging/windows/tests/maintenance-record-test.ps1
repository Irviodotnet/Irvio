# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'maintenance.ps1')
if(Test-Path $Evidence){throw 'Fresh evidence required'}
New-Item -ItemType Directory $Evidence|Out-Null
$results=New-Object 'System.Collections.Generic.List[object]'
function Reject([string]$Name,[scriptblock]$Action){
 $caught=$null;try{& $Action}catch{$caught=$_.Exception.Message}
 if(-not $caught){throw "Unsafe record accepted: $Name"}
 $results.Add(@{test=$Name;result='PASS'})
}
$rows=@([pscustomobject]@{name='IRVIO';start='demand';was_running=$false},[pscustomobject]@{name='IRVIO-Recovery';start='auto';was_running=$false})
$hash='a'*64;$sid='S-1-5-21-101-202-303-1001'
$desktop=[pscustomobject]@{record=[pscustomobject]@{role='desktop';manifest_sha256=$hash;desktop_sid=$sid}}
$record=[pscustomobject]@{schema=1;manifest_sha256=$hash;desktop_sid=$sid;services=$rows}
Assert-IRVIOMaintenanceRecord $record $desktop
$results.Add(@{test='desktop-schema1-and-recovery-layout-compatible';result='PASS'})
$record.desktop_sid+='9'
Reject 'desktop-wrong-owner' {Assert-IRVIOMaintenanceRecord $record $desktop}
$record.desktop_sid=$sid;$record.schema=2
Reject 'desktop-wrong-schema' {Assert-IRVIOMaintenanceRecord $record $desktop}
$record.schema=1
$server=[pscustomobject]@{record=[pscustomobject]@{role='server';manifest_sha256=$hash;bootstrap_sha256=('b'*64)}}
Reject 'desktop-record-not-server' {Assert-IRVIOMaintenanceRecord $record $server}
$sr=[pscustomobject]@{schema=2;role='server';manifest_sha256=$hash;bootstrap_sha256=('b'*64);services=$rows}
Assert-IRVIOMaintenanceRecord $sr $server
$results.Add(@{test='server-schema2-compatible';result='PASS'})
Reject 'server-record-not-desktop' {Assert-IRVIOMaintenanceRecord $sr $desktop}
$sr.bootstrap_sha256='c'*64
Reject 'server-wrong-bootstrap' {Assert-IRVIOMaintenanceRecord $sr $server}
$sr.bootstrap_sha256='b'*64;$sr.services[0].start='boot'
Reject 'invalid-service-start' {Assert-IRVIOMaintenanceRecord $sr $server}
$sr.services[0].start='auto';$sr.services[0].was_running='true'
Reject 'invalid-state-type' {Assert-IRVIOMaintenanceRecord $sr $server}
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'ROLE-AWARE MAINTENANCE RECORD FIXTURES PASS'
