# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [ValidateSet('desktop','server')][string]$Role='desktop',
 [switch]$DevelopmentUnsigned,
 [ValidateSet('','geo-restored','program-restored','metadata-restored','rolled-back','modes-restored','archived')][string]$InjectCrashAfterStep=''
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'software-update.ps1')
if($Role -ceq 'server'){. (Join-Path $PSScriptRoot 'server-installation.ps1')}
if($InjectCrashAfterStep -and -not $DevelopmentUnsigned){throw 'Fault injection requires explicit development recovery'}
function Recovery-Checkpoint([string]$Name){
 if($InjectCrashAfterStep -ceq $Name){[Environment]::Exit(98)}
}
$principal=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if(-not [Environment]::Is64BitProcess -or -not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Elevated 64-bit Windows installer required'}
$root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO'
$program=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO'
$driver=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Exit Driver'
$geo=Join-Path $root 'geo'
$tx=Join-Path $root 'software-update'
$candidate=Read-IRVIOPayload $Payload $ManifestSHA256 ([bool]$DevelopmentUnsigned)
$updateLock=Open-UpdateLock $root
try{
 if((Test-Path (Join-Path $root 'uninstall.json')) -or (Test-Path (Join-Path $root 'uninstalled.json'))){throw 'Uninstall/reinstallation is pending; update recovery refused'}
 if(-not(Test-Path -LiteralPath $tx)){
  Read-IRVIOMaintenanceInstallation $Role|Out-Null
  if(Test-Path (Join-Path $root 'maintenance.json')){throw 'Non-update maintenance is pending; inspect its owner before recovery'}
  return [pscustomobject]@{phase='none';archive=$null;profile_retained=$true;automatic_connection=$false}
 }
 Assert-IRVIOInstallerACL $tx $true
 function Read-UpdateRecord([string]$Path,[bool]$Protected=$false){
  Assert-IRVIOInstallerACL $Path $Protected
  if((Get-Item -LiteralPath $Path).Length -gt 16KB){throw 'Oversized update metadata'}
  return Get-Content -LiteralPath $Path -Raw|ConvertFrom-Json
 }
 $journal=Read-UpdateRecord "$tx\transaction.json" $true
 if($journal.schema -ne 1 -or $journal.id -cnotmatch '^[a-f0-9]{32}$' -or $journal.phase -cnotin @('preparing','prepared','swapping','committed','rolled-back') -or $journal.new_manifest -cne $ManifestSHA256.ToLowerInvariant()){throw 'Unsupported or mismatched update transaction'}
 Assert-IRVIOInstallationRecord $journal.old_record $Role
 foreach($entry in @(@('program_stage','program'),@('program_previous','previous-program'),@('geo_stage','geo'),@('geo_previous','previous-geo'))){
  if($journal.($entry[0]) -cne (Join-Path $tx $entry[1])){throw 'Update journal path differs from canonical transaction layout'}
 }
 $stage=Join-Path $tx 'program';$previous=Join-Path $tx 'previous-program'
 $stageGeo=Join-Path $tx 'geo';$previousGeo=Join-Path $tx 'previous-geo'
 foreach($path in @($program,$geo,$stage,$previous,$stageGeo,$previousGeo)){
  if(Test-Path -LiteralPath $path){Assert-IRVIOInstallerACL $path}
 }
 $newRecord=New-UpdateInstallationRecord $journal.old_record $journal.new_manifest ([bool]$candidate.development_unsigned) $Role
 $recordPath=Join-Path $root 'installation.json'
 $current=Read-UpdateRecord $recordPath $true
 Assert-IRVIOInstallationRecord $current $Role
 $ownerField=if($Role -ceq 'server'){'bootstrap_sha256'}else{'desktop_sid'}
 if($current.$ownerField -cne $journal.old_record.$ownerField -or $current.manifest_sha256 -cnotin @($journal.old_record.manifest_sha256,$journal.new_manifest)){throw 'Installation metadata is unrelated to pending update'}
 $layout=[pscustomobject]@{root=$root;program=$program;driver=$driver;record=$current}
 if($Role -ceq 'server'){
  Assert-IRVIOServerServices $layout (Get-UninstallSCMService 'IRVIO') (Get-UninstallSCMService 'IRVIO-Recovery') (Get-UninstallSCMService 'WinDivert') $true
 }else{Assert-IRVIOInstalledServices $layout (Get-CimInstance Win32_Service -Filter "Name='IRVIO'") (Get-CimInstance Win32_Service -Filter "Name='IRVIO-Recovery'") (Get-CimInstance Win32_SystemDriver -Filter "Name='WinDivert'")}
 $lock=Open-IRVIOMaintenanceLock $layout
 $maintenancePath=Join-Path $root 'maintenance.json'
 $restore=$false
 try{
  if($journal.phase -eq 'committed'){
   $image=Read-IRVIOInstalledImage $root $program $driver $newRecord $geo $Role
   Assert-UpdateImageOwned $image $geo
   if($current.manifest_sha256 -cne $journal.new_manifest){throw 'Committed installation metadata differs'}
  }else{
   $oldProgram=if(Test-Path $previous){$previous}else{$program}
   $oldGeo=if(Test-Path $previousGeo){$previousGeo}else{$geo}
   $image=Read-IRVIOInstalledImage $root $oldProgram $driver $journal.old_record $oldGeo $Role
   Assert-UpdateImageOwned $image $oldGeo
   if($journal.phase -eq 'swapping'){
    $newProgram=if(Test-Path $stage){$stage}else{$program}
    $newGeo=if(Test-Path $stageGeo){$stageGeo}else{$geo}
    $newImage=Read-IRVIOInstalledImage $root $newProgram $driver $newRecord $newGeo $Role
    Assert-UpdateImageOwned $newImage $newGeo
    if(-not(Test-Path $maintenancePath)){throw 'Swapping transaction has lost its maintenance fence'}
   }elseif((Test-Path $previous) -or (Test-Path $previousGeo)){throw 'Unexpected retired image outside swapping/committed phase'}
  }
  if(Test-Path $maintenancePath){
   $maintenance=Read-UpdateRecord $maintenancePath
   if($maintenance.manifest_sha256 -cnotin @($journal.old_record.manifest_sha256,$journal.new_manifest)){throw 'Foreign maintenance digest'}
   $gateRecord=New-UpdateInstallationRecord $journal.old_record $maintenance.manifest_sha256 ([bool]$journal.old_record.development_unsigned) $Role
   $gate=[pscustomobject]@{root=$root;program=$program;driver=$driver;record=$gateRecord}
   Assert-IRVIOMaintenanceRecord $maintenance $gate
   $probe=Join-Path $Payload 'daemon\irviod.exe'
   $hash=($candidate.files|Where-Object path -ceq 'daemon/irviod.exe').sha256
   Assert-IRVIOMaintenanceCleanLocked $gate $probe $hash|Out-Null
   if(Get-CimInstance Win32_Process|Where-Object {$_.ExecutablePath -and ($_.ExecutablePath.StartsWith($program+'\',[StringComparison]::OrdinalIgnoreCase) -or $_.ExecutablePath.StartsWith($tx+'\',[StringComparison]::OrdinalIgnoreCase))}){throw 'Installed/staged processes remain; close them before update recovery'}
  }
  foreach($path in @($recordPath,$maintenancePath,"$tx\transaction.json")){
   $fragment=$path+'.new'
   if(Test-Path -LiteralPath $fragment){
    Assert-IRVIOInstallerACL $fragment
    if((Get-Item $fragment).Length -gt 16KB){throw 'Oversized interrupted metadata fragment'}
    [IO.File]::Move($fragment,(Join-Path $tx ('interrupted-'+[Guid]::NewGuid().ToString('N')+'.json')))
   }
  }
  if($journal.phase -ne 'committed'){
   if(Test-Path $previousGeo){
    if(Test-Path $geo){[IO.Directory]::Move($geo,$stageGeo)}
    [IO.Directory]::Move($previousGeo,$geo)
   }
   Recovery-Checkpoint 'geo-restored'
   if(Test-Path $previous){
    if(Test-Path $program){[IO.Directory]::Move($program,$stage)}
    [IO.Directory]::Move($previous,$program)
   }
   Recovery-Checkpoint 'program-restored'
   Write-UpdateJSON $recordPath $journal.old_record
   if(Test-Path $maintenancePath){$maintenance.manifest_sha256=$journal.old_record.manifest_sha256;Write-UpdateJSON $maintenancePath $maintenance}
   Recovery-Checkpoint 'metadata-restored'
   Read-IRVIOMaintenanceInstallation $Role|Out-Null
   $journal.phase='rolled-back';Write-UpdateJSON "$tx\transaction.json" $journal
   Recovery-Checkpoint 'rolled-back'
  }
  $restore=Test-Path $maintenancePath
 }finally{$lock.Dispose()}
 if($restore){Restore-IRVIOMaintenanceStartModes -Role $Role|Out-Null}
 Recovery-Checkpoint 'modes-restored'
 Read-IRVIOMaintenanceInstallation $Role|Out-Null
 $history=Join-Path $root 'update-history'
 if(-not(Test-Path $history)){New-UpdateDirectory $history}
 Assert-IRVIOInstallerACL $history $true
 $archive=Join-Path $history $journal.id
 if(Test-Path $archive){throw 'Update history ID collision; retained transaction'}
 [IO.Directory]::Move($tx,$archive)
 Recovery-Checkpoint 'archived'
 [pscustomobject]@{phase=$journal.phase;archive=$archive;profile_retained=$true;automatic_connection=$false}
}finally{$updateLock.Dispose()}
