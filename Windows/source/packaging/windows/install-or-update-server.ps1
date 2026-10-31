# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$Bootstrap,
 [switch]$DevelopmentUnsigned
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'server-installation.ps1')
Assert-IRVIOServerBootstrapText $Bootstrap
$root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO'
$recordPath=Join-Path $root 'installation.json'
$options=@{Payload=$Payload;ManifestSHA256=$ManifestSHA256;DevelopmentUnsigned=[bool]$DevelopmentUnsigned}
if(Test-Path (Join-Path $root 'uninstall.json')){throw 'Server removal is pending; resume it before installing or updating'}
if(-not(Test-Path $recordPath) -or (Test-Path (Join-Path $root 'server-copy.json'))){
 & (Join-Path $PSScriptRoot 'install-server.ps1') @options -Bootstrap $Bootstrap
 return
}
Assert-IRVIOInstallerACL $root $true
Assert-IRVIOInstallerACL $recordPath $true
if((Get-Item $recordPath).Length -gt 16KB){throw 'Oversized server installation record'}
$record=Get-Content $recordPath -Raw|ConvertFrom-Json
Assert-IRVIOInstallationRecord $record 'server'
$hash=[Security.Cryptography.SHA256]::Create()
try{$bootstrapHash=[BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::ASCII.GetBytes($Bootstrap))).Replace('-','').ToLowerInvariant()}finally{$hash.Dispose()}
if($record.bootstrap_sha256 -cne $bootstrapHash){throw 'Server update requires the existing bootstrap bundle; changing network identity is not an update operation'}
Read-IRVIOPayload $Payload $ManifestSHA256 ([bool]$DevelopmentUnsigned)|Out-Null
if($null -ne $record.PSObject.Properties['setup_pending']){
 if($record.manifest_sha256 -ine $ManifestSHA256){throw 'Pending server setup requires its original payload; finish setup before updating'}
 $lock=Open-UpdateLock $root
 try{
  $record=Get-Content $recordPath -Raw|ConvertFrom-Json
  Assert-IRVIOServerSetupRecord $record
  if($record.manifest_sha256 -ine $ManifestSHA256 -or $record.bootstrap_sha256 -cne $bootstrapHash){throw 'Pending setup intent changed while acquiring lock'}
  $program=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO'
  $driver=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Exit Driver'
  $installed=Read-IRVIOInstalledImage $root $program $driver $record (Join-Path $root 'geo') 'server'
  Complete-IRVIOServerSetup $installed
 }finally{$lock.Dispose()}
 'IRVIO server installation completed; identity/bootstrap retained and automatic-start service running. M11/GeoIP/selectable Exit readiness remains asynchronous.'
 return
}
if(Test-Path (Join-Path $root 'uninstalled.json')){throw 'Conflicting installation/removal records; inspect the interrupted server reinstall'}
if(Test-Path (Join-Path $root 'software-update')){& (Join-Path $PSScriptRoot 'recover-update.ps1') @options -Role server|Out-Null}
$installed=Read-IRVIOServerInstallation
if($installed.record.bootstrap_sha256 -cne $bootstrapHash){throw 'Server bootstrap changed during setup'}
if(Test-Path (Join-Path $root 'maintenance.json')){throw 'Unrelated maintenance is pending; retain its journal and inspect the previous operation'}
if($installed.record.manifest_sha256 -ine $ManifestSHA256){
 & (Join-Path $PSScriptRoot 'update-desktop.ps1') @options -Role server
 & (Join-Path $PSScriptRoot 'recover-update.ps1') @options -Role server|Out-Null
}
Read-IRVIOServerInstallation|Out-Null
$service=Get-UninstallSCMService IRVIO
if($service.Start -eq 2){
 if($service.State -eq 1){Start-Service IRVIO}
 $watch=[Diagnostics.Stopwatch]::StartNew()
 do{
  $service=Get-UninstallSCMService IRVIO
  if($service.State -eq 1){throw 'Updated server stopped during startup; inspect protected daemon logs and SCM events'}
  if($watch.Elapsed.TotalSeconds -ge 60){throw 'Updated server startup exceeded 60 seconds; inspect SCM and protected daemon logs'}
  if($service.State -ne 4){Start-Sleep -Milliseconds 100}
 }while($service.State -ne 4)
 'IRVIO server package verified/updated; automatic-start service is running. Identity/bootstrap retained. M11/GeoIP/selectable Exit readiness remains asynchronous.'
}else{
 'IRVIO server package verified/updated; existing manual/disabled startup policy retained. No automatic start requested. Identity/bootstrap retained.'
}
