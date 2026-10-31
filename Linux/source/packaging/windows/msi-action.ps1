# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][Alias('A')][ValidateSet('Install','Remove','RollbackInstall','RollbackRemove','Commit')][string]$Action,
 [Parameter(Mandatory=$true)][Alias('H')][ValidatePattern('^[a-fA-F0-9]{64}$')][string]$ManifestSHA256,
 [Alias('D')][string]$DesktopSID='',
 [Alias('P')][string]$InstallDirectory='',
 [Alias('DS')][ValidateSet('','0','1')][string]$DesktopOption='0',
 [Alias('SM')][ValidateSet('','0','1')][string]$StartMenuOption='1'
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'installation.ps1')
$payload=Join-Path $PSScriptRoot 'payload'
$marker=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO\msi-transaction.json'
$params=@{Payload=$payload;ManifestSHA256=$ManifestSHA256;DevelopmentUnsigned=$true}
function Read-Transaction {
 if(!(Test-Path -LiteralPath $marker)){return $null}
 Assert-IRVIOInstallerACL $marker
 $value=Get-Content -LiteralPath $marker -Raw|ConvertFrom-Json
 if($value.manifest -cne $ManifestSHA256.ToLowerInvariant()){throw 'Different MSI transaction is pending'}
 return $value
}
function Write-Transaction([string]$Mode,[string]$SID,$Options) {
 $data=@{manifest=$ManifestSHA256.ToLowerInvariant();action=$Mode;sid=$SID;options=$Options}|ConvertTo-Json -Depth 4
 [IO.File]::WriteAllText($marker,$data,[Text.Encoding]::UTF8)
 Assert-IRVIOInstallerACL $marker
}
try {
 switch($Action){
  'Install' {
   if(Test-Path -LiteralPath $marker){throw 'Pending MSI transaction requires recovery'}
   $options=@{InstallDirectory=$InstallDirectory;DesktopShortcut=($DesktopOption -ceq '1');StartMenuShortcut=($StartMenuOption -ceq '1')}
   & "$PSScriptRoot\install-desktop.ps1" @params @options -DesktopSID $DesktopSID
   Write-Transaction 'installed' $DesktopSID $options
  }
  'Remove' {
   if(Test-Path -LiteralPath $marker){throw 'Pending MSI transaction requires recovery'}
   $installed=Read-IRVIOInstallation
   if($installed.record.manifest_sha256 -cne $ManifestSHA256.ToLowerInvariant()){throw 'MSI does not own this installed payload'}
   $sid=$installed.record.desktop_sid
   $paths=@(Get-IRVIOShortcutPaths $installed.record)
   $options=@{InstallDirectory=$installed.program;DesktopShortcut=((Join-Path ([Environment]::GetFolderPath('CommonDesktopDirectory')) 'IRVIO.lnk') -in $paths);StartMenuShortcut=((Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk') -in $paths)}
   & "$PSScriptRoot\uninstall-desktop.ps1" @params -DesktopSID $sid
   Write-Transaction 'removed' $sid $options
  }
  'RollbackInstall' {
   $tx=Read-Transaction
   if($null -ne $tx -and $tx.action -ceq 'installed'){
    & "$PSScriptRoot\uninstall-desktop.ps1" @params -DesktopSID $tx.sid
    Remove-Item -LiteralPath $marker
   }
  }
  'RollbackRemove' {
   $tx=Read-Transaction
   if($null -ne $tx -and $tx.action -ceq 'removed'){
    $options=@{InstallDirectory=$tx.options.InstallDirectory;DesktopShortcut=[bool]$tx.options.DesktopShortcut;StartMenuShortcut=[bool]$tx.options.StartMenuShortcut}
    & "$PSScriptRoot\install-desktop.ps1" @params @options -DesktopSID $tx.sid
    Remove-Item -LiteralPath $marker
   }
  }
  'Commit' {
   $tx=Read-Transaction
   if($null -ne $tx){Remove-Item -LiteralPath $marker}
  }
 }
 exit 0
}catch{
 Write-Error -ErrorAction Continue $_
 exit 1
}
