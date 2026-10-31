# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$MSI)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$installer=New-Object -ComObject WindowsInstaller.Installer
$db=$installer.OpenDatabase([IO.Path]::GetFullPath($MSI),0)
function Rows([string]$SQL){
 $view=$db.OpenView($SQL)
 try{
  [void]$view.Execute()
  while($record=$view.Fetch()){
   $row=@()
   $count=$record.GetType().InvokeMember('FieldCount','GetProperty',$null,$record,$null)
   for($i=1;$i -le $count;$i++){$row+=,$record.GetType().InvokeMember('StringData','GetProperty',$null,$record,@($i))}
   ,$row
  }
 }finally{[void]$view.Close()}
}
$actions=@{}
foreach($row in (Rows 'SELECT `Action`, `Type`, `Target` FROM `CustomAction`')){$actions[$row[0]]=$row}
foreach($row in $actions.Values){if($row[2].Length -gt 255){throw "Custom action target exceeds MSI column limit: $($row[0])"}}
foreach($name in @('Install','Remove','RollbackInstall','RollbackRemove','Commit')){
 $id=$name+'Backend'
 if(!$actions.ContainsKey($id)){throw "Missing custom action $id"}
 $type=[int]$actions[$id][1]
 if(($type -band 0xC00) -ne 0xC00 -or ($type -band 0x3F) -ne 1){throw "Not a deferred non-impersonating DLL action: $id"}
 if($name.StartsWith('Rollback') -and !($type -band 0x100)){throw 'Rollback flag missing'}
 if($name -eq 'Commit' -and !($type -band 0x200)){throw 'Commit flag missing'}
}
$seq=@{}
foreach($row in (Rows 'SELECT `Action`, `Sequence` FROM `InstallExecuteSequence`')){$seq[$row[0]]=[int]$row[1]}
if(!($seq.InstallFiles -lt $seq.RollbackInstallBackend -and $seq.RollbackInstallBackend -lt $seq.InstallBackend -and $seq.InstallBackend -lt $seq.InstallFinalize)){throw 'Install/rollback ordering is invalid'}
if(!($seq.RollbackRemoveBackend -lt $seq.RemoveBackend -and $seq.RemoveBackend -lt $seq.RemoveFiles)){throw 'Removal/rollback ordering is invalid'}
$conditions=(Rows 'SELECT `Condition` FROM `LaunchCondition`')|ForEach-Object {$_[0]}
if(!($conditions -match 'WINDOWSBUILDREAL')){throw 'Missing real Windows build check'}
if(!($conditions -match 'NOT REINSTALL')){throw 'Unsupported repair is not fenced'}
$secure=(Rows "SELECT ``Value`` FROM ``Property`` WHERE ``Property``='SecureCustomProperties'")[0]
if(($secure -split ';') -notcontains 'IRVIODESKTOPSID'){throw 'Desktop SID is not a secure MSI property'}
foreach($name in @('INSTALLFOLDER','DESKTOPSHORTCUT','STARTMENUSHORTCUT')){
 if(($secure -split ';') -notcontains $name){throw "Missing secure installer preference: $name"}
}
$controls=@(Rows "SELECT ``Control``, ``Type``, ``Property`` FROM ``Control`` WHERE ``Dialog_``='IRVIOShortcutsDlg'")
foreach($name in @('DESKTOPSHORTCUT','STARTMENUSHORTCUT')){
 if(!($controls|Where-Object {$_[1] -ceq 'CheckBox' -and $_[2] -ceq $name})){throw "Missing shortcut checkbox: $name"}
}
$events=@(Rows 'SELECT `Dialog_`, `Control_`, `Event`, `Argument` FROM `ControlEvent`')
if(!($events|Where-Object {$_[0] -ceq 'InstallDirDlg' -and $_[1] -ceq 'Next' -and $_[3] -ceq 'IRVIOShortcutsDlg'})){throw 'Shortcut page is not in the installation workflow'}
$installCommand=$actions['SetInstallBackend'][2]
if($actions['SetCommitBackend'][2] -match '[{}]'){throw 'Commit command contains MSI conditional-formatting braces'}
foreach($property in @('[INSTALLFOLDER]','[DESKTOPSHORTCUT]','[STARTMENUSHORTCUT]')){
 if(!$installCommand.Contains($property)){throw "Installer preference is not passed to backend: $property"}
}
$media=Rows 'SELECT `Cabinet` FROM `Media`'
if(!($media[0] -like '#*')){throw 'Payload cabinet is not embedded'}
$files=@(Rows 'SELECT `File`, `FileName` FROM `File`')
if($files.Count -lt 250){throw 'Incomplete installer payload'}
"MSI PACKAGE PASS: $($files.Count) files; deferred SYSTEM actions, rollback ordering, embedded cabinet, OS/repair gates"
$files | Where-Object {$_[0] -eq 'F783'} | ForEach-Object {"ICE60 informational file: $($_[1])"}
