# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Helper,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. $Helper
New-UpdateDirectory $Evidence
$tree=Join-Path $Evidence 'owned-tree'
New-UpdateDirectory $tree
$file=Join-Path $tree 'owned.txt'
[IO.File]::WriteAllText($file,'owned fixture')
$files=@([pscustomobject]@{path=$file;size=(Get-Item $file).Length;sha256=(Get-FileHash $file).Hash})
$results=New-Object 'System.Collections.Generic.List[object]'
function Pass([string]$Name){$results.Add(@{test=$Name;result='PASS'})}
function Reject([string]$Name,[scriptblock]$Action,[string]$Expected){
 $caught=$null
 try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong rejection for ${Name}: $caught"}
 Pass $Name
}
Assert-UninstallTree $tree $files
Pass 'owned-tree-accepted'
$saved=Join-Path $Evidence 'owned.saved'
[IO.File]::Move($file,$saved)
try{Assert-UninstallTree $tree $files;Pass 'missing-file-accepted-for-resume'}finally{[IO.File]::Move($saved,$file)}
[IO.File]::WriteAllText($file,'changed fixture')
try{
 Reject 'modified-file-refused' {Assert-UninstallTree $tree $files} 'Modified file'
 if([IO.File]::ReadAllText($file) -cne 'changed fixture'){throw 'Inspector modified evidence'}
}finally{[IO.File]::WriteAllText($file,'owned fixture')}
$unknown=Join-Path $tree 'unowned.txt'
[IO.File]::WriteAllText($unknown,'unowned fixture')
try{
 Reject 'unknown-file-refused' {Assert-UninstallTree $tree $files} 'Unowned file'
 if([IO.File]::ReadAllText($unknown) -cne 'unowned fixture'){throw 'Inspector modified unknown file'}
}finally{[IO.File]::Delete($unknown)}
$directory=Join-Path $tree 'unowned-empty-directory'
[void][IO.Directory]::CreateDirectory($directory)
try{Reject 'unknown-empty-directory-refused' {Assert-UninstallTree $tree $files} 'Unowned directory'}finally{[IO.Directory]::Delete($directory)}
$attributes=[IO.File]::GetAttributes($file)
[IO.File]::SetAttributes($file,($attributes -bor [IO.FileAttributes]::ReadOnly))
try{Assert-UninstallTree $tree $files;Pass 'owned-readonly-file-accepted'}finally{[IO.File]::SetAttributes($file,$attributes)}
$acl=Get-Acl $file
$weak=New-Object Security.AccessControl.FileSecurity
$weak.SetSecurityDescriptorSddlForm('O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;GW;;;BU)')
Set-Acl $file $weak
try{Reject 'user-writable-file-refused' {Assert-UninstallTree $tree $files} 'writable by another principal'}finally{Set-Acl $file $acl}
Reject 'foreign-service-name-refused' {Get-UninstallSCMService 'Spooler'} 'Unexpected uninstall service name'
Assert-UninstallTree $tree $files
Pass 'final-owned-tree-unchanged'
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
Get-FileHash $Helper,$PSCommandPath|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'UNINSTALL OWNERSHIP CHECKS PASS; canonical installation untouched'
