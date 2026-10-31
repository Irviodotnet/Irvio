# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$UpdateScript,
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [ValidateSet('desktop','server')][string]$Role='desktop'
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path (Split-Path $UpdateScript) 'maintenance.ps1')
if($Role -ceq 'server'){. (Join-Path (Split-Path $UpdateScript) 'server-installation.ps1')}
$old=Read-IRVIOMaintenanceInstallation $Role
$ownerField=if($Role -ceq 'server'){'bootstrap_sha256'}else{'desktop_sid'}
if((Get-CimInstance Win32_Service -Filter "Name='IRVIO'").State -ne 'Stopped'){throw 'Stopped disposable installation required'}
if(Test-Path $Evidence){throw 'New private evidence directory required'}
New-Item -ItemType Directory $Evidence|Out-Null
$acl=New-Object Security.AccessControl.DirectorySecurity
$acl.SetSecurityDescriptorSddlForm('O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)')
Set-Acl -LiteralPath $Evidence $acl
$tx=Join-Path $old.root 'software-update'
if(Test-Path $tx){throw 'Existing transaction must be preserved/recovered before this gate'}
$results=New-Object 'System.Collections.Generic.List[object]'
function Pass([string]$Name){$results.Add(@{test=$Name;result='PASS'})}
function Reject([string]$Name,[scriptblock]$Action,[string]$Expected){
 $caught=$null
 try{& $Action|Out-Null}catch{$caught=$_.Exception.Message;$_|Out-String|Set-Content "$Evidence\$Name.log"}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong rejection for ${Name}: $caught"}
 Pass $Name
}
function ProfileDigest {
 $profile=Join-Path $old.root 'profile'
 if(-not(Test-Path $profile)){return 'absent'}
 $parts=@(Get-ChildItem $profile -File -Recurse -Force|Sort-Object FullName|ForEach-Object {$_.FullName+'|'+(Get-FileHash $_.FullName).Hash})
 $hash=[Security.Cryptography.SHA256]::Create()
 try{return [BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes(($parts -join "`n")))).Replace('-','')}finally{$hash.Dispose()}
}
function ServiceState {
 return (@('IRVIO','IRVIO-Recovery')|ForEach-Object {
  $service=Get-CimInstance Win32_Service -Filter "Name='$_'"
  "$($service.Name)|$($service.State)|$(Get-IRVIOMaintenanceStart $service)"
 }) -join "`n"
}
$profile=ProfileDigest;$services=ServiceState
function Assert-Baseline {
 $restored=Read-IRVIOMaintenanceInstallation $Role
 if($restored.record.manifest_sha256 -cne $old.record.manifest_sha256 -or (ProfileDigest) -cne $profile -or (ServiceState) -cne $services){throw 'Rejected update changed baseline, profile or service modes'}
 if(Test-Path (Join-Path $old.root 'maintenance.json')){throw 'Maintenance modes not restored'}
}
Reject 'wrong-manifest' {& $UpdateScript -Role $Role -Payload $Payload -ManifestSHA256 ('0'*64) -DevelopmentUnsigned} 'manifest hash mismatch'
Assert-Baseline
Reject 'development-payload-in-release' {& $UpdateScript -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256} 'Development payload is not permitted'
Assert-Baseline
Reject 'release-fault-injection' {& $UpdateScript -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256 -InjectFailureAfterStep metadata} 'Fault injection requires'
Assert-Baseline
$sentinel=Join-Path $old.program 'update-gate-unowned.txt'
$file=[IO.File]::Open($sentinel,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
try{$bytes=[Text.Encoding]::UTF8.GetBytes('Unowned update gate fixture');$file.Write($bytes,0,$bytes.Length)}finally{$file.Dispose()}
$sentinelHash=(Get-FileHash $sentinel).Hash
try{
 Reject 'unowned-file' {& $UpdateScript -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned} 'Unowned file in update target'
 Assert-Baseline
 if((Get-FileHash $sentinel).Hash -ne $sentinelHash -or (Test-Path $tx)){throw 'Preflight altered unowned file or created transaction'}
}finally{
 if((Get-FileHash $sentinel).Hash -ne $sentinelHash){throw 'Refusing to remove changed test sentinel'}
 Remove-Item -LiteralPath $sentinel
}
foreach($fault in @('program-swap','geo-swap','metadata')){
 Reject "rollback-$fault" {& $UpdateScript -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned -InjectFailureAfterStep $fault} "^Injected $fault failure$"
 Assert-Baseline
 $journal=Get-Content -Raw "$tx\transaction.json"|ConvertFrom-Json
 if($journal.phase -cne 'rolled-back'){throw 'Rollback transaction not terminal'}
 Move-Item -LiteralPath $tx -Destination "$Evidence\rollback-$fault"
}
& $UpdateScript -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned
$new=Read-IRVIOMaintenanceInstallation $Role
if($new.record.manifest_sha256 -ine $ManifestSHA256 -or $new.record.$ownerField -cne $old.record.$ownerField -or (ProfileDigest) -cne $profile -or (ServiceState) -cne $services){throw 'Update changed profile, authorization or service state'}
Pass 'successful-update-profile-authorization-modes-retained'
$journal=Get-Content -Raw "$tx\transaction.json"|ConvertFrom-Json
if($journal.phase -cne 'committed'){throw 'Commit record missing'}
Reject 'unfinished-transaction-reuse' {& $UpdateScript -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned} 'Previous update transaction exists'
Stop-IRVIOForMaintenance -Role $Role|Out-Null
$probe=Join-Path $Payload 'daemon\irviod.exe'
try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash -Role $Role|ConvertTo-Json|Set-Content "$Evidence\clean.json"}finally{Restore-IRVIOMaintenanceStartModes -Role $Role|Out-Null}
Pass 'updated-fenced-runtime-clean'
$results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\results.json"
Get-FileHash $UpdateScript,$PSCommandPath,(Join-Path $Payload 'payload-manifest.json')|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'SOFTWARE UPDATE CHECKS PASS; synchronous rollback only; retained transaction, no power-loss acceptance'
