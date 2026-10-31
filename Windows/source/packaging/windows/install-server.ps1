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
 [switch]$DevelopmentUnsigned,
 [ValidateSet('','copy-intent','copy-partial','copy-file','copy-complete','copy-record','record','kernel','recovery','service','configured','started')][string]$DevelopmentStopAfter=''
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'server-installation.ps1')
$principal=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if(-not [Environment]::Is64BitProcess -or -not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Elevated 64-bit Windows installer required'}
if([Environment]::OSVersion.Version.Build -lt 22000){throw 'Windows 11 or newer is required'}
Assert-IRVIOServerBootstrapText $Bootstrap
$manifest=Read-IRVIOPayload $Payload $ManifestSHA256 ([bool]$DevelopmentUnsigned)
if($DevelopmentStopAfter -and (-not $DevelopmentUnsigned -or -not $manifest.development_unsigned)){throw 'Installation interruption is restricted to development payloads'}
$Payload=(Get-Item -LiteralPath $Payload).FullName
$root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO'
$program=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO'
$driver=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Exit Driver'
$shortcut=Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk'
$hash=[Security.Cryptography.SHA256]::Create()
try{$bootstrapHash=[BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::ASCII.GetBytes($Bootstrap))).Replace('-','').ToLowerInvariant()}finally{$hash.Dispose()}
$copyPath=Join-Path $root 'server-copy.json'
$copyRecord=$null
$preserved=$null
$lock=$null
if(Test-Path $root){
 if(Test-Path $copyPath){$copyRecord=Read-IRVIOServerCopyRecord $root $ManifestSHA256 $bootstrapHash $manifest.development_unsigned}
 else{$preserved=Read-IRVIOPreservedServerInstallation $root $program $Bootstrap}
 $lock=Open-UpdateLock $root
}
try{
if($null -eq $copyRecord){
if($null -ne $preserved){
 $preserved=Read-IRVIOPreservedServerInstallation $root $program $Bootstrap
 foreach($name in @('installation.json','uninstall.json','maintenance.json','software-update','geo')){
  if(Test-Path (Join-Path $root $name)){throw 'Retained server data has unfinished maintenance or installation objects'}
 }
}
foreach($path in @($root,$program,$driver,$shortcut)){
 if($path -ceq $root -and $null -ne $preserved){continue}
 Assert-IRVIOPlainPath ([IO.Path]::GetDirectoryName($path))
 if(Test-Path -LiteralPath $path){throw 'Fresh server installation requires unused IRVIO paths; existing desktop/server data will not be overwritten'}
}
foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw "Existing service must not be replaced: $name"}}
Assert-IRVIOServerPortsAvailable
 if($null -eq $preserved){New-IRVIOServerDirectory $root}
 if($null -eq $lock){$lock=Open-UpdateLock $root}
 $receiptHash='';$retainedManifestHash=''
 if($null -ne $preserved){
  Read-IRVIOPreservedServerInstallation $root $program $Bootstrap|Out-Null
  $receiptHash=(Get-FileHash "$root\uninstalled.json").Hash.ToLowerInvariant()
  $retainedManifestHash=(Get-FileHash "$root\uninstall-manifest.json").Hash.ToLowerInvariant()
 }
 $record=@{schema=1;role='server';manifest_sha256=$ManifestSHA256.ToLowerInvariant();bootstrap_sha256=$bootstrapHash;development_unsigned=[bool]$manifest.development_unsigned;setup_pending=$true;setup_retained_receipt_sha256=$receiptHash;setup_retained_manifest_sha256=$retainedManifestHash}
 Write-UpdateJSON $copyPath $record
}
try{
 $copyRecord=Read-IRVIOServerCopyRecord $root $ManifestSHA256 $bootstrapHash $manifest.development_unsigned
 $layout=[pscustomobject]@{record=$copyRecord;root=$root;program=$program;driver=$driver}
 Complete-IRVIOServerCopy $layout $manifest $Payload $Bootstrap $DevelopmentStopAfter
 $installed=Read-IRVIOInstalledImage $root $program $driver $copyRecord (Join-Path $root 'geo') 'server'
 Complete-IRVIOServerSetup $installed $DevelopmentStopAfter
}catch{
 throw "Server installation/start did not complete: $($_.Exception.Message). Owned partial state is retained; do not delete runtime protection. Rerun the same server installer/payload/bootstrap if server-copy.json or installation.json exists. Otherwise retain state for inspection. Inspect: Get-Service IRVIO,IRVIO-Recovery; Get-WinEvent -LogName Application -MaxEvents 30."
}
'IRVIO server service is running with automatic startup; no UI installed. M11 quorum, GeoIP and selectable Exit readiness are separate asynchronous gates, not yet an acceptance result.'
}finally{if($null -ne $lock){$lock.Dispose()}}
