# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Helper,
 [Parameter(Mandatory=$true)][string]$ProbePath,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. $Helper
if(Test-Path $Evidence){throw 'New evidence required'}
New-Item -ItemType Directory $Evidence|Out-Null
$installed=Read-IRVIOInstallation
if((Get-CimInstance Win32_Service -Filter "Name='IRVIO'").State -ne 'Stopped'){throw 'Stopped lab fixture required'}
$digest=(Get-FileHash $ProbePath).Hash
$results=New-Object 'System.Collections.Generic.List[object]'
function Reject([string]$Name,[scriptblock]$Action,[string]$Expected){
 $caught=$null
 try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong rejection for ${Name}: $caught"}
 $results.Add(@{test=$Name;result='PASS'})
}
$probe=Start-Process $ProbePath -ArgumentList '--maintenance-check' -Wait -PassThru -RedirectStandardOutput "$Evidence\unfenced.log" -RedirectStandardError "$Evidence\unfenced-error.log"
if($probe.ExitCode -eq 0){throw 'Unfenced command accepted'}
$results.Add(@{test='unfenced-command-refused';result='PASS'})
Stop-IRVIOForMaintenance|Out-Null
try{
 Reject 'wrong-probe-digest' {Assert-IRVIOMaintenanceClean $ProbePath ('0'*64)} 'digest mismatch'
 $clean=Assert-IRVIOMaintenanceClean $ProbePath $digest
 $clean|ConvertTo-Json|Set-Content "$Evidence\clean.json"
 $results.Add(@{test='fenced-installed-runtime-clean';result='PASS'})
 $intent=Join-Path $installed.root 'runtime\session-state'
 $sentinel=[Text.Encoding]::UTF8.GetBytes('maintenance inspection fixture; not a usable intent')
 $file=[IO.File]::Open($intent,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
 try{$file.Write($sentinel,0,$sentinel.Length)}finally{$file.Dispose()}
 $acl=New-Object Security.AccessControl.FileSecurity
 $acl.SetSecurityDescriptorSddlForm('O:SYG:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)')
 Set-Acl $intent $acl
 $intentHash=(Get-FileHash $intent).Hash
 try{
  Reject 'intent-residue' {Assert-IRVIOMaintenanceClean $ProbePath $digest} 'session intent or teardown marker remains'
  if((Get-FileHash $intent).Hash -ne $intentHash){throw 'Inspector changed intent fixture'}
  $results.Add(@{test='intent-residue-preserved';result='PASS'})
 }finally{
  if((Get-FileHash $intent).Hash -ne $intentHash){throw 'Refusing to remove changed intent fixture'}
  Remove-Item -LiteralPath $intent
 }
 $ruleName='IRVIO.Maintenance.Fixture.Only'
 if(Get-NetFirewallRule -Name $ruleName -ErrorAction SilentlyContinue){throw 'Foreign fixture already exists'}
 New-NetFirewallRule -Name $ruleName -DisplayName $ruleName -Group 'IRVIO.Inbound.v1' -Direction Inbound -Action Block -Protocol TCP -LocalAddress 127.0.0.1 -LocalPort 65534|Out-Null
 try{
  Reject 'orphan-firewall-rule' {Assert-IRVIOMaintenanceClean $ProbePath $digest} 'firewall rules remain'
  if(-not(Get-NetFirewallRule -Name $ruleName -ErrorAction Stop)){throw 'Inspector removed firewall evidence'}
  $results.Add(@{test='orphan-rule-preserved';result='PASS'})
 }finally{Remove-NetFirewallRule -Name $ruleName}
 Assert-IRVIOMaintenanceClean $ProbePath $digest|Out-Null
 $results.Add(@{test='clean-after-fixture-removal';result='PASS'})
}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
if((Get-CimInstance Win32_Service -Filter "Name='IRVIO'").State -ne 'Stopped'){throw 'Unexpected daemon restart'}
$results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\results.json"
Get-FileHash $ProbePath,$Helper,$PSCommandPath|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'MAINTENANCE CLEAN INSPECTION PASS; no installed files removed'
