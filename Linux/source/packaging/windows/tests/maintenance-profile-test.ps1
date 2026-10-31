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
$profile=Join-Path $installed.root 'profile'
if(-not(Test-Path "$profile\identity.key")){throw 'Disposable populated profile required; this test erases it'}
$digest=(Get-FileHash $ProbePath).Hash
$identity=(Get-FileHash "$profile\identity.key").Hash
$results=New-Object 'System.Collections.Generic.List[object]'
function Reject([string]$Name,[scriptblock]$Action,[string]$Expected){
 $caught=$null
 try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong rejection for ${Name}: $caught"}
 if((Get-FileHash "$profile\identity.key").Hash -cne $identity){throw 'Refusal changed profile identity'}
 $results.Add(@{test=$Name;result='PASS'})
}
Reject 'unfenced-profile-erase-refused' {Invoke-IRVIOMaintenanceProbe $ProbePath $digest 'remove-profile'} 'stopped, disabled'
Stop-IRVIOForMaintenance|Out-Null
try{
 $layout=Read-IRVIOInstallation
 $maintenance=Open-IRVIOMaintenanceLock $layout
 try{
  Assert-IRVIOMaintenanceCleanLocked $layout $ProbePath $digest|Out-Null
  $runtimeLock=Join-Path $installed.root 'runtime\product.lock'
  $held=[IO.File]::Open($runtimeLock,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
  try{Reject 'runtime-lock-refuses-profile-erase' {Invoke-IRVIOMaintenanceProbe $ProbePath $digest 'remove-profile'} 'lock unavailable'}finally{$held.Dispose()}
  foreach($case in @(@{name='active-intent';path=(Join-Path $installed.root 'runtime\session-state');reason='intent or teardown marker remains'},@{name='unknown-profile-file';path=(Join-Path $profile 'unowned-erase-fixture.txt');reason='unrecognized entry'})){
   $file=[IO.File]::Open($case.path,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
   try{$bytes=[Text.Encoding]::UTF8.GetBytes('profile erase refusal fixture');$file.Write($bytes,0,$bytes.Length)}finally{$file.Dispose()}
   $acl=New-Object Security.AccessControl.FileSecurity
   $acl.SetSecurityDescriptorSddlForm('O:SYG:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)')
   Set-Acl $case.path $acl
   $sentinel=(Get-FileHash $case.path).Hash
   try{Reject $case.name {Invoke-IRVIOMaintenanceProbe $ProbePath $digest 'remove-profile'} $case.reason}finally{
    if((Get-FileHash $case.path).Hash -cne $sentinel){throw 'Refusal fixture changed; retain evidence'}
    Remove-Item -LiteralPath $case.path
   }
  }
  foreach($attempt in 1..2){
   Invoke-IRVIOMaintenanceProbe $ProbePath $digest 'remove-profile'
   if(@(Get-ChildItem $profile -Force).Count){throw 'Profile contents remain'}
   $results.Add(@{test="erase-owned-profile-attempt-$attempt";result='PASS'})
  }
  Read-IRVIOInstallation|Out-Null
  Assert-IRVIOMaintenanceCleanLocked $layout $ProbePath $digest|ConvertTo-Json|Set-Content "$Evidence\clean.json"
  $results.Add(@{test='installed-image-and-native-state-preserved';result='PASS'})
 }finally{$maintenance.Dispose()}
}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
Get-FileHash $Helper,$ProbePath,$PSCommandPath|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'NATIVE PROFILE ERASE PASS; disposable profile deleted, services remain stopped'
