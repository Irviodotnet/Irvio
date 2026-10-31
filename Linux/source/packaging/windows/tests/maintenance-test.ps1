# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Helper,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. $Helper
if(Test-Path $Evidence){throw 'New evidence required'}
New-Item -ItemType Directory $Evidence|Out-Null
$installed=Read-IRVIOInstallation
if((Get-CimInstance Win32_Service -Filter "Name='IRVIO'").State -ne 'Stopped'){throw 'Stopped disposable fixture required'}
$results=New-Object 'System.Collections.Generic.List[object]'
function Pass([string]$Name){$results.Add(@{test=$Name;result='PASS'})}
function Reject([string]$Name,[scriptblock]$Action,[string]$Expected){
 $caught=$null
 try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong rejection for ${Name}: $caught"}
 Pass $Name
}
$record=Stop-IRVIOForMaintenance
foreach($name in @('IRVIO','IRVIO-Recovery')){
 $s=Get-CimInstance Win32_Service -Filter "Name='$name'"
 if($s.State -ne 'Stopped' -or $s.StartMode -ne 'Disabled'){throw 'Service not fenced'}
}
Pass 'idle-stop-fences-services'
Reject 'duplicate-operation' {Stop-IRVIOForMaintenance} 'Pending maintenance'
$serialized=$record|ConvertTo-Json -Depth 4
$bad=$serialized|ConvertFrom-Json;$bad.schema=2
Reject 'unknown-schema' {Assert-IRVIOMaintenanceRecord $bad $installed} 'does not match'
$bad=$serialized|ConvertFrom-Json;$bad.desktop_sid+='9'
Reject 'wrong-user' {Assert-IRVIOMaintenanceRecord $bad $installed} 'does not match'
$bad=$serialized|ConvertFrom-Json;$bad.services[1].name='UnrelatedService'
Reject 'foreign-service' {Assert-IRVIOMaintenanceRecord $bad $installed} 'Invalid maintenance service'
$bad=$serialized|ConvertFrom-Json;$bad.services[0].start='boot'
Reject 'invalid-start-mode' {Assert-IRVIOMaintenanceRecord $bad $installed} 'Invalid maintenance service'
$bad=$serialized|ConvertFrom-Json;$bad.services[0].was_running='true'
Reject 'invalid-state-type' {Assert-IRVIOMaintenanceRecord $bad $installed} 'Invalid maintenance service'
$lock=Open-IRVIOMaintenanceLock $installed
try{Reject 'concurrent-restore' {Restore-IRVIOMaintenanceStartModes} 'being used by another process'}finally{$lock.Dispose()}
Restore-IRVIOMaintenanceStartModes|Out-Null
if(Test-Path (Join-Path $installed.root 'maintenance.json')){throw 'Maintenance record not cleared'}
Pass 'durable-start-modes-restored'
$realSC=(Get-Item Function:Invoke-IRVIOMaintenanceSC).ScriptBlock
$script:scCalls=0
try{
 function Invoke-IRVIOMaintenanceSC([string[]]$Arguments){
  $script:scCalls++
  if($script:scCalls -eq 2){throw 'Injected second service configuration failure'}
  & $realSC $Arguments
 }
 Reject 'partial-disable-failure' {Stop-IRVIOForMaintenance} 'Injected second service'
}finally{Set-Item Function:Invoke-IRVIOMaintenanceSC $realSC}
if((Get-CimInstance Win32_Service -Filter "Name='IRVIO'").StartMode -ne 'Disabled'){throw 'Fault did not occur after first disable'}
$pending=Read-IRVIOMaintenanceRecord $installed
if($pending.services[0].start -ne 'demand'){throw 'Pre-failure start mode was lost'}
Pass 'partial-failure-retains-original-modes'
Restore-IRVIOMaintenanceStartModes|Out-Null
if((Get-CimInstance Win32_Service -Filter "Name='IRVIO'").StartMode -ne 'Manual'){throw 'Failed operation restoration failed'}
Pass 'partial-failure-restoration'
Invoke-IRVIOMaintenanceSC @('start','IRVIO')
$watch=[Diagnostics.Stopwatch]::StartNew()
do{
 $s=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
 if($watch.Elapsed.TotalSeconds -gt 30){throw 'Daemon start timeout'}
 if($s.State -ne 'Running'){Start-Sleep -Milliseconds 100}
}while($s.State -ne 'Running')
$record=Stop-IRVIOForMaintenance
if(-not $record.services[0].was_running){throw 'Running service state not captured'}
Pass 'running-daemon-gracefully-stopped'
Restore-IRVIOMaintenanceStartModes|Out-Null
if((Get-CimInstance Win32_Service -Filter "Name='IRVIO'").State -ne 'Stopped'){throw 'Maintenance unexpectedly restarted VPN daemon'}
Pass 'restore-does-not-reconnect'
$after=Read-IRVIOInstallation
if($after.record.manifest_sha256 -ne $installed.record.manifest_sha256){throw 'Maintenance modified payload'}
Pass 'installed-payload-unchanged'
$results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\results.json"
Get-FileHash $Helper,$PSCommandPath|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'MAINTENANCE STOP CHECKS PASS (not full cleanup, upgrade or uninstall acceptance)'
