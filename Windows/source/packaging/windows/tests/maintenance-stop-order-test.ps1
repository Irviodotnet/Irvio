# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. "$Scripts\maintenance.ps1"
if(Test-Path $Evidence){throw 'New evidence required'}
New-Item -ItemType Directory $Evidence|Out-Null
$script:installed=[pscustomobject]@{root=$Evidence;program=$Evidence;record=[pscustomobject]@{role='desktop';manifest_sha256=('a'*64);desktop_sid='S-1-5-21-101-202-303-1001'}}
function Read-IRVIOMaintenanceInstallation {return $script:installed}
function Open-IRVIOMaintenanceLock {return New-Object IO.MemoryStream}
function Assert-IRVIOInstallerACL {}
function Get-CimInstance([string]$ClassName,[string]$Filter){
 if($ClassName -ne 'Win32_Service' -and $ClassName -ne 'Win32_Process'){throw 'Unexpected CIM class'}
 switch -Exact ($Filter){
  "Name='IRVIO'" {return $script:services.IRVIO}
  "Name='IRVIO-Recovery'" {return $script:services.'IRVIO-Recovery'}
  "Name='irviod.exe'" {return}
  default {throw 'Unexpected CIM query'}
 }
}
function Invoke-IRVIOMaintenanceSC([string[]]$Arguments){
 $script:operations.Add(($Arguments -join ' '))
 $service=$script:services[$Arguments[1]]
 if($Arguments[0] -eq 'config'){
  if($Arguments.Count -ne 4 -or $Arguments[2] -ne 'start=' -or $Arguments[3] -ne 'disabled'){throw 'Unexpected SCM config'}
  $service.StartMode='Disabled'
 }elseif($Arguments[0] -eq 'stop'){
  if($Arguments[1] -eq 'IRVIO' -and $script:services.'IRVIO-Recovery'.StartMode -ne 'Auto'){throw 'Recovery was disabled before WFP teardown'}
  if(-not(Test-Path "$Evidence\maintenance.json")){throw 'Service mutation without durable maintenance intent'}
  $service.State='Stopped';$service.ProcessId=0
  if($script:failStop -and $Arguments[1] -eq 'IRVIO'){$service.ExitCode=1066}
 }else{throw 'Unexpected SCM operation'}
}
function ResetFixture([bool]$Failure){
 $script:failStop=$Failure
 $script:operations=New-Object 'System.Collections.Generic.List[string]'
 $script:services=@{
  IRVIO=[pscustomobject]@{State='Running';StartMode='Manual';DelayedAutoStart=$false;ProcessId=123;ExitCode=0}
  'IRVIO-Recovery'=[pscustomobject]@{State='Stopped';StartMode='Auto';DelayedAutoStart=$false;ProcessId=0;ExitCode=1077}
 }
}
$results=New-Object 'System.Collections.Generic.List[object]'
ResetFixture $false
$record=Stop-IRVIOForMaintenance
$expected=@('config IRVIO start= disabled','stop IRVIO','config IRVIO-Recovery start= disabled')
if(($script:operations -join '|') -cne ($expected -join '|')){throw 'Wrong maintenance stop ordering'}
if($record.services[1].start -ne 'auto' -or -not $record.services[0].was_running){throw 'Original modes were not recorded'}
$results.Add(@{test='recovery-automatic-through-daemon-stop';result='PASS'})
Move-Item "$Evidence\maintenance.json" "$Evidence\successful-record.json"
ResetFixture $true
$caught=$null
try{Stop-IRVIOForMaintenance|Out-Null}catch{$caught=$_.Exception.Message}
if($caught -notmatch 'Service stopped with a failure'){throw 'Failed daemon stop was accepted'}
if($script:services.'IRVIO-Recovery'.StartMode -ne 'Auto' -or $script:services.IRVIO.StartMode -ne 'Disabled'){throw 'Failure did not preserve recovery while fencing main daemon'}
if(-not(Test-Path "$Evidence\maintenance.json")){throw 'Failed teardown lost maintenance intent'}
$results.Add(@{test='failed-teardown-retains-automatic-recovery-and-journal';result='PASS'})
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'MAINTENANCE STOP ORDER FIXTURE PASS; real connected update is a separate gate'
