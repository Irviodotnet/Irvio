# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$DesktopSID,
 [switch]$DevelopmentUnsigned
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'installation.ps1')
$root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO'
$recordPath=Join-Path $root 'installation.json'
$options=@{Payload=$Payload;ManifestSHA256=$ManifestSHA256;DevelopmentUnsigned=[bool]$DevelopmentUnsigned}
if(Test-Path (Join-Path $root 'uninstall.json')){throw 'An uninstall is pending; resume it before installing or updating'}
if(-not(Test-Path -LiteralPath $recordPath)){
 & (Join-Path $PSScriptRoot 'install-desktop.ps1') @options -DesktopSID $DesktopSID
 return
}
Assert-IRVIOInstallerACL $root $true
Assert-IRVIOInstallerACL $recordPath $true
if((Get-Item -LiteralPath $recordPath).Length -gt 16KB){throw 'Installation metadata is oversized'}
$record=Get-Content -LiteralPath $recordPath -Raw|ConvertFrom-Json
Assert-IRVIOInstallationRecord $record
if($DesktopSID -cne $record.desktop_sid){throw 'Launch this installer as the existing authorized IRVIO desktop user; update cannot change that identity'}
Read-IRVIOPayload $Payload $ManifestSHA256 ([bool]$DevelopmentUnsigned)|Out-Null
if(Test-Path (Join-Path $root 'software-update')){
 & (Join-Path $PSScriptRoot 'recover-update.ps1') @options|Out-Null
}
$installed=Read-IRVIOInstallation
if($installed.record.desktop_sid -cne $DesktopSID){throw 'Desktop authorization changed during setup'}
if(Test-Path (Join-Path $root 'maintenance.json')){throw 'Unrelated maintenance is pending; retain its journal and inspect the previous operation'}
if($installed.record.manifest_sha256 -ieq $ManifestSHA256){
 'Verified IRVIO package already installed; no service or profile changes.'
 return
}
& (Join-Path $PSScriptRoot 'update-desktop.ps1') @options
& (Join-Path $PSScriptRoot 'recover-update.ps1') @options|Out-Null
'IRVIO update finalized; existing desktop authorization and profile retained.'
