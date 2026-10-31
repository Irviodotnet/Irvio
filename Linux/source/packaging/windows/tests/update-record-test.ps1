# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'software-update.ps1')
if(Test-Path $Evidence){throw 'Fresh evidence required'}
New-Item -ItemType Directory $Evidence|Out-Null
$results=New-Object 'System.Collections.Generic.List[object]'
foreach($role in @('desktop','server')){
 $old=[ordered]@{schema=1;role=$role;manifest_sha256=('a'*64);development_unsigned=$true}
 $owner=if($role -eq 'server'){'bootstrap_sha256'}else{'desktop_sid'}
 $value=if($role -eq 'server'){'b'*64}else{'S-1-5-21-101-202-303-1001'}
 $old[$owner]=$value
 $old=[pscustomobject]$old
 $new=New-UpdateInstallationRecord $old ('C'*64) $false $role
 if($new.role -cne $role -or $new.$owner -cne $value -or $new.manifest_sha256 -cne ('c'*64) -or $new.development_unsigned -ne $false){throw 'Update record lost intent or owner'}
 if($old.manifest_sha256 -cne ('a'*64) -or $old.development_unsigned -ne $true){throw 'Update record mutated original ownership'}
 $results.Add(@{test="$role-owner-preserved-original-unchanged";result='PASS'})
 $other=if($role -eq 'server'){'desktop'}else{'server'}
 $caught=$null;try{New-UpdateInstallationRecord $old ('c'*64) $true $other|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught){throw 'Cross-role conversion accepted'}
 $results.Add(@{test="$role-cross-role-refusal";result='PASS'})
 $caught=$null;try{New-UpdateInstallationRecord $old 'bad' $true $role|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught){throw 'Invalid new manifest accepted'}
 $results.Add(@{test="$role-invalid-manifest-refusal";result='PASS'})
}
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'UPDATE RECORD ROLE COMPATIBILITY PASS'
