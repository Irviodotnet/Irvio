# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. "$PSScriptRoot\..\installation.ps1"
$default=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO'
if((Get-IRVIOProgramDirectory ([pscustomobject]@{})) -cne $default){throw 'Legacy directory fallback changed'}
if(@(Get-IRVIOShortcutPaths ([pscustomobject]@{})).Count -ne 1){throw 'Legacy shortcut fallback changed'}
foreach($desktop in @($false,$true)){
 foreach($start in @($false,$true)){
  $paths=@(Get-IRVIOShortcutPaths ([pscustomobject]@{desktop_shortcut=$desktop;start_menu_shortcut=$start}))
  if($paths.Count -ne ([int]$desktop+[int]$start)){throw 'Shortcut choices are not independent'}
 }
}
foreach($bad in @('C:\','relative','\\server\share\IRVIO','C:\Apps\..\IRVIO','C:\Apps\IRVIO" -Command evil','C:\Windows\IRVIO','C:\ProgramData\IRVIO')){
 $rejected=$false
 try{Resolve-IRVIOProgramDirectory $bad|Out-Null}catch{$rejected=$true}
 if(!$rejected){throw "Unsafe application directory accepted: $bad"}
}
$custom=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Custom Test'
if((Resolve-IRVIOProgramDirectory ($custom+'\.')) -cne $custom){throw 'Directory normalization failed'}
Assert-IRVIOInstallParent ([Environment]::GetFolderPath('ProgramFiles'))
$parameters=(Get-Command "$PSScriptRoot\..\msi-action.ps1").Parameters
foreach($item in @(@('DesktopSID','D'),@('InstallDirectory','P'),@('DesktopOption','DS'),@('StartMenuOption','SM'),@('Action','A'))){
 if($parameters[$item[0]].Aliases -notcontains $item[1]){throw 'MSI action uses an ambiguous parameter abbreviation'}
}
'DESKTOP_OPTIONS_PASS: legacy defaults, four shortcut combinations, protected directory validation, explicit parameter aliases'
