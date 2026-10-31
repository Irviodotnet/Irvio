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
$root=Join-Path $Evidence 'state';New-IRVIOServerDirectory $root
$program=Join-Path $Evidence 'program';$driver=Join-Path $Evidence 'driver'
$bootstrap='irvio-bootstrap:v1:QUJD'
[IO.File]::WriteAllText("$root\server-bootstrap.txt",$bootstrap,[Text.Encoding]::ASCII)
Protect-UpdateFile "$root\server-bootstrap.txt"
$record=[pscustomobject]@{schema=1;role='server';manifest_sha256=('a'*64);bootstrap_sha256=(Get-FileHash "$root\server-bootstrap.txt").Hash.ToLowerInvariant();development_unsigned=$true;setup_pending=$true;setup_retained_receipt_sha256='';setup_retained_manifest_sha256=''}
$installed=[pscustomobject]@{record=$record;root=$root;program=$program;driver=$driver}
$command=Get-IRVIOServerCommand $installed
$service=[pscustomobject]@{Path=$command;Account='LocalSystem';Type=16;Start=4;State=1}
$recovery=[pscustomobject]@{Path=('"'+$program+'\daemon\irviod.exe" --boot-recover');Account='LocalSystem';Type=16;Start=3;State=1}
$kernel=[pscustomobject]@{Path=('"'+$driver+'\WinDivert64.sys"');Type=1;Start=3;State=1}
$results=New-Object 'System.Collections.Generic.List[object]'
function Pass([string]$Name,[scriptblock]$Action){& $Action|Out-Null;$results.Add(@{test=$Name;result='PASS'})}
function Reject([string]$Name,[scriptblock]$Action,[string]$Pattern){
 $caught=$null;try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Pattern){throw "Wrong refusal for ${Name}: $caught"}
 $results.Add(@{test=$Name;result='PASS'})
}
Pass 'pending-record-valid' {Assert-IRVIOServerSetupRecord $record}
$record.setup_pending=$false
Reject 'pending-false' {Assert-IRVIOServerSetupRecord $record} 'missing or invalid'
$record.setup_pending='true'
Reject 'pending-string' {Assert-IRVIOServerSetupRecord $record} 'missing or invalid'
$record.setup_pending=$true
$record.setup_retained_receipt_sha256='a'*64
Reject 'incomplete-retained-binding' {Assert-IRVIOServerSetupRecord $record} 'Incomplete retained'
$record.setup_retained_receipt_sha256='../receipt'
Reject 'invalid-retained-binding' {Assert-IRVIOServerSetupRecord $record} 'Invalid retained'
$record.setup_retained_receipt_sha256=''
Pass 'all-services-missing' {Assert-IRVIOServerSetupServices $installed $null $null $null}
Pass 'partial-kernel-only' {Assert-IRVIOServerSetupServices $installed $null $null $kernel}
Pass 'partial-kernel-recovery' {Assert-IRVIOServerSetupServices $installed $null $recovery $kernel}
Pass 'all-services-stopped' {Assert-IRVIOServerSetupServices $installed $service $recovery $kernel}
$service.Path='C:\unrelated.exe'
Reject 'foreign-service' {Assert-IRVIOServerSetupServices $installed $service $recovery $kernel} 'ownership/start state'
$service.Path=$command;$service.Account='LocalService'
Reject 'foreign-account' {Assert-IRVIOServerSetupServices $installed $service $recovery $kernel} 'ownership/start state'
$service.Account='LocalSystem';$service.Start=3
Reject 'externally-changed-start' {Assert-IRVIOServerSetupServices $installed $service $recovery $kernel} 'ownership/start state'
$service.Start=2;$service.State=4;$kernel.State=4
Pass 'already-running-complete-set' {Assert-IRVIOServerSetupServices $installed $service $recovery $kernel}
Reject 'running-missing-recovery' {Assert-IRVIOServerSetupServices $installed $service $null $kernel} 'incomplete service set'
Reject 'running-missing-driver' {Assert-IRVIOServerSetupServices $installed $service $recovery $null} 'incomplete service set'
$service.State=1;$service.Start=4;$kernel.State=1;$recovery.Start=2
Reject 'auto-recovery-refused' {Assert-IRVIOServerSetupServices $installed $service $recovery $kernel} 'recovery service ownership'
$recovery.Start=3;$recovery.State=4
Reject 'active-recovery-refused' {Assert-IRVIOServerSetupServices $installed $service $recovery $kernel} 'recovery service ownership'
$recovery.State=1;$kernel.Path='C:\unrelated.sys'
Reject 'foreign-driver-refused' {Assert-IRVIOServerSetupServices $installed $service $recovery $kernel} 'driver ownership'
$kernel.Path='"'+$driver+'\WinDivert64.sys"';$kernel.Start=0
Reject 'boot-driver-refused' {Assert-IRVIOServerSetupServices $installed $service $recovery $kernel} 'driver ownership'
$kernel.Start=3
Pass 'no-retained-files' {Assert-IRVIOServerSetupRetainedFiles $installed}
Write-UpdateJSON "$root\uninstalled.json" @{fixture='receipt'}
Write-UpdateJSON "$root\uninstall-manifest.json" @{fixture='manifest'}
Reject 'unexpected-receipt-refused' {Assert-IRVIOServerSetupRetainedFiles $installed} 'metadata changed'
$record.setup_retained_receipt_sha256=(Get-FileHash "$root\uninstalled.json").Hash.ToLowerInvariant()
$record.setup_retained_manifest_sha256=(Get-FileHash "$root\uninstall-manifest.json").Hash.ToLowerInvariant()
Pass 'retained-metadata-bound' {Assert-IRVIOServerSetupRecord $record;Assert-IRVIOServerSetupRetainedFiles $installed}
[IO.File]::AppendAllText("$root\uninstalled.json",' ')
Reject 'changed-retained-metadata-refused' {Assert-IRVIOServerSetupRetainedFiles $installed} 'metadata changed'
Remove-Item -LiteralPath "$root\uninstalled.json"
Pass 'partial-receipt-consumption' {Assert-IRVIOServerSetupRetainedFiles $installed}
Remove-Item -LiteralPath "$root\uninstall-manifest.json"
Pass 'completed-receipt-consumption' {Assert-IRVIOServerSetupRetainedFiles $installed}
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'SERVER PENDING SETUP FIXTURES PASS; no canonical paths or SCM modified'
