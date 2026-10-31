# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [switch]$DevelopmentUnsigned,
 [switch]$RemoveProfile,
 [ValidateSet('desktop','server')][string]$Role='desktop',
 [string]$DesktopSID='',
 [ValidateSet('','removing','first-file','files-removed','first-service')][string]$InjectCrashAfterStep=''
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'uninstall.ps1')
if($Role -ceq 'server'){
 . (Join-Path $PSScriptRoot 'server-installation.ps1')
 if($DesktopSID){throw 'Server uninstall does not accept a desktop user SID'}
}
if($DesktopSID -and $DesktopSID -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$'){throw 'Explicit desktop user SID required'}
if($InjectCrashAfterStep -and -not $DevelopmentUnsigned){throw 'Uninstall fault injection requires explicit development mode'}
function Uninstall-Checkpoint([string]$Name){if($InjectCrashAfterStep -ceq $Name){[Environment]::Exit(97)}}
$principal=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if(-not [Environment]::Is64BitProcess -or -not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Elevated 64-bit Windows installer required'}
$candidate=Read-IRVIOPayload $Payload $ManifestSHA256 ([bool]$DevelopmentUnsigned)
$root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO'
$program=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO'
$driver=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Exit Driver'
$geo=Join-Path $root 'geo'
$shortcut=Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk'
$shortcuts=@($shortcut)
if($Role -ceq 'desktop'){
 $deployment=Read-IRVIODeploymentRecord $root
 $program=Get-IRVIOProgramDirectory $deployment
 $shortcuts=@(Get-IRVIOShortcutPaths $deployment)
}
$journalPath=Join-Path $root 'uninstall.json'
$manifestCopy=Join-Path $root 'uninstall-manifest.json'
$receipt=Join-Path $root 'uninstalled.json'
$updateLock=Open-UpdateLock $root
try{
 if(Test-Path (Join-Path $root 'software-update')){throw 'Update transaction must be resolved before removal'}
 if(Test-Path $receipt){
  $completed=Read-UninstallJournal $receipt $Role
  if($DesktopSID -and $completed.record.desktop_sid -cne $DesktopSID){throw 'Uninstall belongs to another desktop user'}
  if($completed.phase -cne 'completed' -or (Test-Path $journalPath)){throw 'Conflicting uninstall records'}
  if($completed.retain_profile -eq [bool]$RemoveProfile){throw 'Uninstall profile choice differs from recorded intent'}
  if(-not $completed.retain_profile){Assert-IRVIOErasedProfile $root}
  foreach($path in (@($program,$driver,$geo)+$shortcuts)){if(Test-Path $path){throw 'Application objects appeared after uninstall'}}
  foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'A service appeared after uninstall'}}
  'IRVIO already uninstalled; recorded profile choice verified.'
  return
 }
 if(-not(Test-Path $journalPath)){
  $installed=Read-IRVIOMaintenanceInstallation $Role
  if($DesktopSID -and $installed.record.desktop_sid -cne $DesktopSID){throw 'Uninstall belongs to another desktop user'}
  $shortcutHash=''
  $shortcutRecords=@()
  if($Role -ceq 'desktop'){
   foreach($shortcut in $shortcuts){
   Assert-UninstallShortcut $shortcut $program
   $shortcutHash=(Get-FileHash $shortcut).Hash.ToLowerInvariant()
   $shortcutRecords+=@{path=$shortcut;sha256=$shortcutHash}
   }
  }elseif(Test-Path $shortcut){throw 'Unexpected desktop shortcut in server installation'}
  if(Test-Path $manifestCopy){throw 'Prior uninstall manifest requires inspection'}
  Copy-Item -LiteralPath (Join-Path $program 'payload-manifest.json') -Destination $manifestCopy
  Protect-UpdateFile $manifestCopy
  $schema=if($Role -ceq 'server'){2}else{1}
  $journal=@{schema=$schema;phase='prepared';record=$installed.record;retain_profile=(-not [bool]$RemoveProfile);shortcut_sha256=$shortcutHash}
  if($Role -ceq 'desktop'){$journal.shortcuts=$shortcutRecords}
  Write-UpdateJSON $journalPath $journal
 }
 $journal=Read-UninstallJournal $journalPath $Role
 if($DesktopSID -and $journal.record.desktop_sid -cne $DesktopSID){throw 'Uninstall belongs to another desktop user'}
 if($journal.retain_profile -eq [bool]$RemoveProfile){throw 'Uninstall profile choice differs from recorded intent'}
 $manifest=Read-IRVIOInstalledManifest $manifestCopy $journal.record $Role
 $layout=[pscustomobject]@{root=$root;program=$program;driver=$driver;record=$journal.record}
 $files=@(Get-UninstallFiles $layout $manifest $manifestCopy)
 $roots=@($program,$driver,$geo)
 foreach($path in $roots){Assert-UninstallTree $path $files}
 foreach($entry in @(Get-UninstallShortcuts $journal)){
 $shortcut=$entry.path
 if(Test-Path $shortcut){
  if($Role -ceq 'server'){throw 'Unexpected desktop shortcut in server installation'}
  Assert-UninstallShortcut $shortcut $program
  if((Get-FileHash $shortcut).Hash -ine $entry.sha256){throw 'Shortcut changed since uninstall was prepared'}
 }elseif($Role -ceq 'desktop' -and $journal.phase -ceq 'prepared'){throw 'Owned shortcut disappeared before removal'}
 }
 if(Get-CimInstance Win32_Process|Where-Object {$_.ExecutablePath -and $_.ExecutablePath.StartsWith($program+'\',[StringComparison]::OrdinalIgnoreCase) -and $_.Name -ine 'irviod.exe'}){throw 'Close the installed UI and bridge before uninstalling'}
 if($journal.phase -ceq 'prepared'){
  if(-not(Test-Path (Join-Path $root 'maintenance.json'))){Stop-IRVIOForMaintenance -Role $Role|Out-Null}
 }
 $lock=Open-IRVIOMaintenanceLock $layout
 try{
  if($journal.phase -cin @('prepared','removing')){
   $probe=Join-Path $Payload 'daemon\irviod.exe'
   $hash=($candidate.files|Where-Object path -ceq 'daemon/irviod.exe').sha256
   Assert-IRVIOMaintenanceCleanLocked $layout $probe $hash|Out-Null
   $kernel=Assert-UninstallService $layout 'WinDivert' $false $true
   if($kernel.State -eq 4){Invoke-IRVIOMaintenanceSC @('stop','WinDivert')}
   foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){Assert-UninstallService $layout $name $false|Out-Null}
   if(Get-CimInstance Win32_Process|Where-Object {$_.ExecutablePath -and $_.ExecutablePath.StartsWith($program+'\',[StringComparison]::OrdinalIgnoreCase)}){throw 'Installed process remains after uninstall stop'}
   if(-not $journal.retain_profile){
    Invoke-IRVIOMaintenanceProbe $probe $hash 'remove-profile'
    Assert-IRVIOErasedProfile $root
   }
   $journal.phase='removing';Write-UpdateJSON $journalPath $journal
   Uninstall-Checkpoint 'removing'
   $removed=0
   foreach($file in $files){
    if(-not(Test-Path -LiteralPath $file.path)){continue}
    Assert-IRVIOInstallerACL $file.path
    $item=Get-Item -LiteralPath $file.path
    if($item -isnot [IO.FileInfo] -or $item.Length -ne $file.size -or (Get-FileHash $file.path).Hash -ine $file.sha256){throw 'Owned uninstall file changed before removal'}
    Remove-Item -LiteralPath $file.path -Force
    if(++$removed -eq 1){Uninstall-Checkpoint 'first-file'}
   }
   $directories=New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
   foreach($file in $files){
    $parent=[IO.Path]::GetDirectoryName($file.path)
    while($parent -and $parent -notin $roots){[void]$directories.Add($parent);$parent=[IO.Path]::GetDirectoryName($parent)}
   }
   foreach($path in (@($directories|Sort-Object Length -Descending)+$roots)){
    if(Test-Path $path){Assert-IRVIOInstallerACL $path;[IO.Directory]::Delete($path)}
   }
   foreach($entry in @(Get-UninstallShortcuts $journal)){
   $shortcut=$entry.path
   if(Test-Path $shortcut){
    if($Role -ceq 'server'){throw 'Unexpected desktop shortcut in server installation'}
    Assert-UninstallShortcut $shortcut $program
    if((Get-FileHash $shortcut).Hash -ine $entry.sha256){throw 'Shortcut changed before removal'}
    Remove-Item -LiteralPath $shortcut
   }
   }
   $journal.phase='files-removed';Write-UpdateJSON $journalPath $journal
   Uninstall-Checkpoint 'files-removed'
  }
  if($journal.phase -cnotin @('files-removed','completed')){throw 'Unsupported uninstall completion state'}
  foreach($path in (@($roots)+$shortcuts)){if(Test-Path $path){throw 'Owned application files remain before service removal'}}
  $removedServices=0
  foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){
   $service=Assert-UninstallService $layout $name $true
   if($null -ne $service){
    Invoke-IRVIOMaintenanceSC @('delete',$name)
    if($null -ne (Get-UninstallSCMService $name)){throw 'Service deletion pending; close service viewers and rerun uninstall'}
    if(++$removedServices -eq 1){Uninstall-Checkpoint 'first-service'}
   }
  }
  foreach($name in @('maintenance.json','installation.json')){
   $path=Join-Path $root $name
   if(Test-Path $path){Assert-IRVIOInstallerACL $path;Remove-Item -LiteralPath $path}
  }
  $journal.phase='completed';Write-UpdateJSON $journalPath $journal
  [IO.File]::Move($journalPath,$receipt)
 }finally{$lock.Dispose()}
 if($journal.retain_profile){'IRVIO application/services removed; profile retained in protected ProgramData\IRVIO.'}else{'IRVIO application/services removed; profile erased. Configuration and diagnostic history retained in protected ProgramData\IRVIO.'}
}finally{$updateLock.Dispose()}
