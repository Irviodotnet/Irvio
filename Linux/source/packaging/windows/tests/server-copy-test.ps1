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
$program=Join-Path $Evidence 'program';New-IRVIOServerDirectory $program
$source=Join-Path $Evidence 'source.txt'
[IO.File]::WriteAllText($source,('fixture only, never executable '*20),[Text.Encoding]::ASCII)
$file=[pscustomobject]@{source=$source;path=(Join-Path $program 'one.txt');sha256=(Get-FileHash $source).Hash;size=(Get-Item $source).Length}
$results=New-Object 'System.Collections.Generic.List[object]'
function Pass([string]$Name,[scriptblock]$Action){& $Action|Out-Null;$results.Add(@{test=$Name;result='PASS'})}
function Reject([string]$Name,[scriptblock]$Action,[string]$Pattern){
 $caught=$null;try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Pattern){throw "Wrong refusal for ${Name}: $caught"}
 $results.Add(@{test=$Name;result='PASS'})
}
Pass 'missing-tree' {Assert-IRVIOServerPartialTree "$Evidence\missing" @($file)}
Pass 'empty-owned-tree' {Assert-IRVIOServerPartialTree $program @($file)}
Reject 'partial-copy-injection' {Copy-IRVIOServerFile $file $true} 'development interruption: copy-partial'
if((Test-Path $file.path) -or (Get-Item ($file.path+'.irvio-install-part')).Length -ne 64){throw 'Partial fixture was published as final'}
Pass 'known-partial-tree' {Assert-IRVIOServerPartialTree $program @($file)}
Pass 'resume-partial-copy' {Copy-IRVIOServerFile $file}
Pass 'idempotent-copy' {Copy-IRVIOServerFile $file}
if((Get-FileHash $file.path).Hash -ine $file.sha256 -or (Test-Path ($file.path+'.irvio-install-part'))){throw 'Copy completion mismatch'}
$readonly=$file|ConvertTo-Json|ConvertFrom-Json;$readonly.path=Join-Path $program 'readonly.txt'
[IO.File]::SetAttributes($source,([IO.File]::GetAttributes($source) -bor [IO.FileAttributes]::ReadOnly))
Pass 'readonly-source-copy' {Copy-IRVIOServerFile $readonly}
if(-not ((Get-Item $source).Attributes -band [IO.FileAttributes]::ReadOnly)){throw 'Copy changed source attributes'}
Remove-Item -LiteralPath $readonly.path
[IO.File]::WriteAllText(($readonly.path+'.irvio-install-part'),'partial')
[IO.File]::SetAttributes(($readonly.path+'.irvio-install-part'),[IO.FileAttributes]::ReadOnly)
Pass 'readonly-partial-resume' {Copy-IRVIOServerFile $readonly}
Remove-Item -LiteralPath $readonly.path
[IO.File]::SetAttributes($source,([IO.File]::GetAttributes($source) -band (-bnot [IO.FileAttributes]::ReadOnly)))
[IO.File]::AppendAllText($file.path,'tampered')
Reject 'completed-tamper-refused' {Copy-IRVIOServerFile $file} 'Completed server copy differs'
Copy-Item $source $file.path -Force;Protect-UpdateFile $file.path
[IO.File]::WriteAllText(($file.path+'.irvio-install-part'),'ambiguous')
Reject 'final-and-partial-refused' {Copy-IRVIOServerFile $file} 'Both completed and partial'
Remove-Item -LiteralPath ($file.path+'.irvio-install-part')
[IO.File]::WriteAllText("$program\foreign.txt",'not owned')
Reject 'foreign-file-refused' {Assert-IRVIOServerPartialTree $program @($file)} 'Unowned object'
Remove-Item -LiteralPath "$program\foreign.txt"
New-IRVIOServerDirectory "$program\foreign"
Reject 'foreign-empty-directory-refused' {Assert-IRVIOServerPartialTree $program @($file)} 'Unowned object'
Remove-Item -LiteralPath "$program\foreign"
$root=Join-Path $Evidence 'root';New-IRVIOServerDirectory $root
$record=[pscustomobject]@{schema=1;role='server';manifest_sha256=('a'*64);bootstrap_sha256=('b'*64);development_unsigned=$true;setup_pending=$true;setup_retained_receipt_sha256='';setup_retained_manifest_sha256=''}
Write-UpdateJSON "$root\server-copy.json" $record
Pass 'bound-copy-record' {Read-IRVIOServerCopyRecord $root ('a'*64) ('b'*64) $true}
Reject 'wrong-package-refused' {Read-IRVIOServerCopyRecord $root ('c'*64) ('b'*64) $true} 'original payload/bootstrap/signing mode'
Reject 'wrong-bootstrap-refused' {Read-IRVIOServerCopyRecord $root ('a'*64) ('c'*64) $true} 'original payload/bootstrap/signing mode'
Reject 'wrong-signing-mode-refused' {Read-IRVIOServerCopyRecord $root ('a'*64) ('b'*64) $false} 'original payload/bootstrap/signing mode'
$layout=[pscustomobject]@{root=$root;program=$program;driver="$Evidence\driver";record=$record}
Pass 'missing-installed-record' {Assert-IRVIOServerCopyInstallationRecord $layout}
Write-UpdateJSON "$root\installation.json" $record
Pass 'matching-pending-record' {Assert-IRVIOServerCopyInstallationRecord $layout}
$other=$record|ConvertTo-Json|ConvertFrom-Json;$other.bootstrap_sha256='c'*64
Write-UpdateJSON "$root\installation.json" $other
Reject 'conflicting-installed-record-refused' {Assert-IRVIOServerCopyInstallationRecord $layout} 'conflicts with installation record'
$bad=[pscustomobject]@{files=@([pscustomobject]@{path='daemon/x.irvio-install-part';sha256=('c'*64);size=1})}
Reject 'reserved-payload-suffix-refused' {Get-IRVIOServerCopyFiles $layout $bad $Evidence} 'reserved installation suffix'
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'SERVER COPY FIXTURES PASS; no canonical paths or services modified'
