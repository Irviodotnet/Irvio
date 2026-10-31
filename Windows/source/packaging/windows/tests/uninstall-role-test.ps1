# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
New-IRVIOServerDirectory $Evidence
$results=New-Object 'System.Collections.Generic.List[object]'
function Reject([string]$Name,[scriptblock]$Action){
 $caught=$false;try{& $Action|Out-Null}catch{$caught=$true}
 if(-not $caught){throw "Expected refusal: $Name"}
 $results.Add(@{test=$Name;result='PASS'})
}
foreach($role in @('desktop','server')){
 $record=[ordered]@{schema=1;role=$role;manifest_sha256=('a'*64);development_unsigned=$true}
 if($role -ceq 'server'){$record.bootstrap_sha256='b'*64}else{$record.desktop_sid='S-1-5-21-101-202-303-1001'}
 $schema=if($role -ceq 'server'){2}else{1}
 $shortcut=if($role -ceq 'server'){''}else{'c'*64}
 $journal=@{schema=$schema;phase='prepared';record=$record;retain_profile=$true;shortcut_sha256=$shortcut}
 $path=Join-Path $Evidence "$role.json"
 Write-UpdateJSON $path $journal
 $actual=Read-UninstallJournal $path $role
 if($actual.record.role -cne $role -or -not $actual.retain_profile){throw 'Journal lost role or profile intent'}
 $results.Add(@{test="$role-journal";result='PASS'})
 $other=if($role -ceq 'server'){'desktop'}else{'server'}
 Reject "$role-cross-role-refused" {Read-UninstallJournal $path $other}
 $journal.schema=9;Write-UpdateJSON $path $journal
 Reject "$role-unknown-schema-refused" {Read-UninstallJournal $path $role}
 $journal.schema=$schema;$journal.shortcut_sha256=if($role -ceq 'server'){'c'*64}else{''}
 Write-UpdateJSON $path $journal
 Reject "$role-shortcut-contract-refused" {Read-UninstallJournal $path $role}
 $journal.shortcut_sha256=$shortcut;$journal.retain_profile='false';Write-UpdateJSON $path $journal
 Reject "$role-profile-choice-type-refused" {Read-UninstallJournal $path $role}
 $layout=[pscustomobject]@{root="$Evidence\state";program="$Evidence\program";driver="$Evidence\driver";record=[pscustomobject]$record}
 $manifest=[pscustomobject]@{files=@([pscustomobject]@{path='daemon/irviod.exe';sha256=('d'*64);size=1},[pscustomobject]@{path='ui/irvio.exe';sha256=('e'*64);size=1})}
 $files=@(Get-UninstallFiles $layout $manifest $path)
 $expected=if($role -ceq 'server'){2}else{3}
 if($files.Count -ne $expected){throw 'Wrong role-specific uninstall file projection'}
 $results.Add(@{test="$role-file-projection";result='PASS'})
}
$root=Join-Path $Evidence 'state';New-IRVIOServerDirectory $root
[IO.File]::WriteAllText("$root\server-bootstrap.txt",'irvio-bootstrap:v1:QUJD',[Text.Encoding]::ASCII)
Protect-UpdateFile "$root\server-bootstrap.txt"
$layout.record.bootstrap_sha256=(Get-FileHash "$root\server-bootstrap.txt").Hash.ToLowerInvariant()
$command=Get-IRVIOServerCommand $layout
$script:fixtureService=[pscustomobject]@{Path=$command;Type=16;Account='LocalSystem';Start=4;ProcessId=0;State=1}
# Fixture-only SCM substitution; no installed service is read or modified.
function Get-UninstallSCMService([string]$Name){return $script:fixtureService}
Assert-UninstallService $layout IRVIO $false|Out-Null
$results.Add(@{test='server-exact-command-fenced';result='PASS'})
$script:fixtureService.Path+=' --country XX'
Reject 'server-foreign-command-refused' {Assert-UninstallService $layout IRVIO $false}
$script:fixtureService.Path=$command;$script:fixtureService.Start=2
Reject 'server-unfenced-service-refused' {Assert-UninstallService $layout IRVIO $false}
$script:fixtureService.Start=4;$script:fixtureService.ProcessId=123
Reject 'server-live-process-refused' {Assert-UninstallService $layout IRVIO $false}
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'UNINSTALL ROLE FIXTURES PASS; no canonical installation modified'
