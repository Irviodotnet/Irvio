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
 [ValidateSet('','program-swap','geo-swap','metadata')][string]$InjectFailureAfterStep='',
 [ValidateSet('','prepared','fenced','program-retired','program-swap','geo-retired','geo-swap','metadata','committed')][string]$InjectCrashAfterStep=''
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'software-update.ps1')
if($Role -ceq 'server'){. (Join-Path $PSScriptRoot 'server-installation.ps1')}
if(($InjectFailureAfterStep -or $InjectCrashAfterStep) -and -not $DevelopmentUnsigned){throw 'Fault injection requires an explicit development update'}
function Update-Checkpoint([string]$Name){
 if($InjectCrashAfterStep -ceq $Name){[Environment]::Exit(99)}
}
$installed=Read-IRVIOMaintenanceInstallation $Role
$candidate=Read-IRVIOPayload $Payload $ManifestSHA256 ([bool]$DevelopmentUnsigned)
$Payload=(Get-Item -LiteralPath $Payload).FullName
$updateLock=Open-UpdateLock $installed.root
try{
$installed=Read-IRVIOMaintenanceInstallation $Role
if((Test-Path (Join-Path $installed.root 'uninstall.json')) -or (Test-Path (Join-Path $installed.root 'uninstalled.json'))){throw 'Uninstall/reinstallation is pending; do not update this installation'}
$oldDrivers=@($installed.manifest.files|Where-Object {$_.path.StartsWith('driver/')}|ForEach-Object {"$($_.path)|$($_.sha256)|$($_.size)"}|Sort-Object)
$newDrivers=@($candidate.files|Where-Object {$_.path.StartsWith('driver/')}|ForEach-Object {"$($_.path)|$($_.sha256)|$($_.size)"}|Sort-Object)
if(@(Compare-Object $oldDrivers $newDrivers).Count){throw 'Driver payload changes require the separate driver-upgrade gate'}
function Assert-UpdateProcessesStopped([bool]$AllowDaemon){
 if(Get-CimInstance Win32_Process|Where-Object {$_.ExecutablePath -and $_.ExecutablePath.StartsWith($installed.program+'\',[StringComparison]::OrdinalIgnoreCase) -and (-not $AllowDaemon -or $_.Name -ne 'irviod.exe')}){throw 'Close the IRVIO UI and bridge before updating; installed processes remain'}
}
Assert-UpdateProcessesStopped $true
$tx=Join-Path $installed.root 'software-update'
if(Test-Path -LiteralPath $tx){throw 'Previous update transaction exists; recover or inspect it before updating'}
if([IO.Path]::GetPathRoot($installed.program) -ine [IO.Path]::GetPathRoot($installed.root)){throw 'Cross-volume program/state installation requires a separate update gate'}

$components=if($Role -ceq 'server'){'^(daemon|licenses)/'}else{'^(daemon|ui|licenses)/'}
$programFiles=@('payload-manifest.json')+@($installed.manifest.files|Where-Object {$_.path -match $components}|ForEach-Object path)
$geoFiles=@($installed.manifest.files|Where-Object {$_.path.StartsWith('geo/')}|ForEach-Object {$_.path.Substring(4)})
Assert-OwnedTree $installed.program $programFiles
Assert-OwnedTree (Join-Path $installed.root 'geo') $geoFiles
$id=[Guid]::NewGuid().ToString('N')
$stage=Join-Path $tx 'program'
$previous=Join-Path $tx 'previous-program'
$stageGeo=Join-Path $tx 'geo'
$previousGeo=Join-Path $tx 'previous-geo'
$journal=$null;$locked=$null;$fenced=$false;$committed=$false
$programMoved=$false;$programInstalled=$false;$geoMoved=$false;$geoInstalled=$false
try{
 New-UpdateDirectory $tx
 $journal=@{schema=1;phase='preparing';id=$id;old_record=$installed.record;new_manifest=$ManifestSHA256.ToLowerInvariant();program_stage=$stage;program_previous=$previous;geo_stage=$stageGeo;geo_previous=$previousGeo}
 Write-UpdateJSON "$tx\transaction.json" $journal
 New-UpdateDirectory $stage ($Role -ceq 'desktop')
 New-UpdateDirectory "$stage\daemon"
 New-UpdateDirectory $stageGeo
 foreach($file in $candidate.files){
  $parts=$file.path.Split('/',2)
  if($parts[0] -eq 'driver' -or ($Role -ceq 'server' -and $parts[0] -eq 'ui')){continue}
  $target=if($parts[0] -eq 'geo'){Join-Path $stageGeo $parts[1]}else{Join-Path $stage $file.path}
  New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($target))|Out-Null
  Copy-Item -LiteralPath (Join-Path $Payload $file.path) -Destination $target
  if((Get-FileHash -LiteralPath $target).Hash -ne $file.sha256){throw 'Staged payload digest changed'}
  if($parts[0] -in @('daemon','geo')){Protect-UpdateFile $target}
 }
 Copy-Item -LiteralPath (Join-Path $Payload 'payload-manifest.json') -Destination "$stage\payload-manifest.json"
 if((Get-FileHash "$stage\payload-manifest.json").Hash -ne $ManifestSHA256){throw 'Staged manifest digest changed'}
 $journal.phase='prepared';Write-UpdateJSON "$tx\transaction.json" $journal
 Update-Checkpoint 'prepared'
 Stop-IRVIOForMaintenance -Role $Role|Out-Null;$fenced=$true
 $locked=Open-IRVIOMaintenanceLock $installed
 $probe=Join-Path $stage 'daemon\irviod.exe'
 $probeHash=($candidate.files|Where-Object path -ceq 'daemon/irviod.exe').sha256
 Assert-IRVIOMaintenanceCleanLocked $installed $probe $probeHash|Out-Null
 $current=Read-IRVIOMaintenanceInstallation $Role
 if($current.record.manifest_sha256 -ne $installed.record.manifest_sha256){throw 'Installed baseline changed while staging'}
 Assert-OwnedTree $installed.program $programFiles
 Assert-OwnedTree (Join-Path $installed.root 'geo') $geoFiles
 Assert-UpdateProcessesStopped $false
 $journal.phase='swapping';Write-UpdateJSON "$tx\transaction.json" $journal
 Update-Checkpoint 'fenced'
 [IO.Directory]::Move($installed.program,$previous);$programMoved=$true
 Update-Checkpoint 'program-retired'
 [IO.Directory]::Move($stage,$installed.program);$programInstalled=$true
 Update-Checkpoint 'program-swap'
 if($InjectFailureAfterStep -eq 'program-swap'){throw 'Injected program-swap failure'}
 [IO.Directory]::Move((Join-Path $installed.root 'geo'),$previousGeo);$geoMoved=$true
 Update-Checkpoint 'geo-retired'
 [IO.Directory]::Move($stageGeo,(Join-Path $installed.root 'geo'));$geoInstalled=$true
 Update-Checkpoint 'geo-swap'
 if($InjectFailureAfterStep -eq 'geo-swap'){throw 'Injected geo-swap failure'}
 $newRecord=New-UpdateInstallationRecord $installed.record $ManifestSHA256 ([bool]$candidate.development_unsigned) $Role
 Write-UpdateJSON (Join-Path $installed.root 'installation.json') $newRecord
 Update-Checkpoint 'metadata'
 if($InjectFailureAfterStep -eq 'metadata'){throw 'Injected metadata failure'}
 $updated=Read-IRVIOMaintenanceInstallation $Role
 $maintenance=Read-IRVIOMaintenanceRecord $installed
 $maintenance.manifest_sha256=$newRecord.manifest_sha256
 Write-UpdateJSON (Join-Path $installed.root 'maintenance.json') $maintenance
 $journal.phase='committed';Write-UpdateJSON "$tx\transaction.json" $journal
 $committed=$true
 Update-Checkpoint 'committed'
}catch{
 $failure=$_
 try{
 if(-not $committed){
  if($geoInstalled){[IO.Directory]::Move((Join-Path $installed.root 'geo'),$stageGeo)}
  if($geoMoved){[IO.Directory]::Move($previousGeo,(Join-Path $installed.root 'geo'))}
  if($programInstalled){[IO.Directory]::Move($installed.program,$stage)}
  if($programMoved){[IO.Directory]::Move($previous,$installed.program)}
  if($programMoved){
   Write-UpdateJSON (Join-Path $installed.root 'installation.json') $installed.record
   $maintenance=Get-Content -Raw (Join-Path $installed.root 'maintenance.json')|ConvertFrom-Json
   $maintenance.manifest_sha256=$installed.record.manifest_sha256
   Write-UpdateJSON (Join-Path $installed.root 'maintenance.json') $maintenance
   Read-IRVIOMaintenanceInstallation $Role|Out-Null
  }
  if($journal){$journal.phase='rolled-back';Write-UpdateJSON "$tx\transaction.json" $journal}
 }
 }catch{throw "Software update failed: $($failure.Exception.Message); rollback incomplete: $($_.Exception.Message)"}
 throw $failure
}finally{
 if($locked){$locked.Dispose()}
 if($fenced -and ($committed -or $journal.phase -eq 'rolled-back')){Restore-IRVIOMaintenanceStartModes -Role $Role|Out-Null}
}
'IRVIO software update installed; profile retained, daemon stopped; transaction backups retained for recovery validation.'
}finally{$updateLock.Dispose()}
