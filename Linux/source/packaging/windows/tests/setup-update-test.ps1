# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$SetupEXE,
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'software-update.ps1')
$old=Read-IRVIOInstallation
if($old.record.manifest_sha256 -ieq $ManifestSHA256){throw 'A different candidate manifest is required'}
foreach($name in @('IRVIO','IRVIO-Recovery')){if((Get-Service $name).Status -ne 'Stopped'){throw 'Stopped disposable fixture required'}}
New-UpdateDirectory $Evidence
$tx=Join-Path $old.root 'software-update'
if(Test-Path $tx){throw 'Previous transaction still pending'}
function ProfileDigest {
 $parts=@(Get-ChildItem (Join-Path $old.root 'profile') -File -Recurse -Force|Sort-Object FullName|ForEach-Object {$_.FullName+'|'+(Get-FileHash $_.FullName).Hash})
 $hash=[Security.Cryptography.SHA256]::Create()
 try{return [BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes(($parts -join "`n")))).Replace('-','')}finally{$hash.Dispose()}
}
function ServiceState {
 return (@('IRVIO','IRVIO-Recovery')|ForEach-Object {
  $s=Get-CimInstance Win32_Service -Filter "Name='$_'"
  "$($s.Name)|$($s.State)|$(Get-IRVIOMaintenanceStart $s)"
 }) -join "`n"
}
function RunLabProcess([string]$File,[string]$Arguments,[int]$Expected,[string]$Log){
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName=$File;$start.Arguments=$Arguments
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $process=New-Object Diagnostics.Process
 $process.StartInfo=$start
 try{
  if(-not $process.Start()){throw 'Lab child did not start'}
  $stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
  if(-not $process.WaitForExit(240000)){$process.Kill();throw 'Lab child timeout; inspect pending installer state'}
  [IO.File]::WriteAllText("$Evidence\$Log.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Evidence\$Log-error.log",$stderr.GetAwaiter().GetResult())
  if($process.ExitCode -ne $Expected){throw "Wrong child result for ${Log}: $($process.ExitCode)"}
 }finally{$process.Dispose()}
}
$profile=ProfileDigest;$services=ServiceState
$results=New-Object 'System.Collections.Generic.List[object]'
$updateArgs='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+(Join-Path $Scripts 'update-desktop.ps1')+'" -Payload "'+$Payload+'" -ManifestSHA256 '+$ManifestSHA256+' -DevelopmentUnsigned -InjectCrashAfterStep program-retired'
RunLabProcess "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe" $updateArgs 99 'interrupted-update'
if(Test-Path $old.program){throw 'Missing-program fixture not reached'}
$journalHash=(Get-FileHash "$tx\transaction.json").Hash
RunLabProcess $SetupEXE '--internal-install-for S-1-5-21-101-202-303-9999 --development-unattended' 1 'wrong-user'
if((Get-FileHash "$tx\transaction.json").Hash -ne $journalHash -or (Test-Path $old.program)){throw 'Wrong-user setup modified pending update'}
$results.Add(@{test='wrong-user-refused-before-recovery';result='PASS'})
$setupArgs='--internal-install-for '+$old.record.desktop_sid+' --development-unattended'
RunLabProcess $SetupEXE $setupArgs 0 'recovery-and-update'
$new=Read-IRVIOInstallation
if($new.record.manifest_sha256 -ine $ManifestSHA256 -or $new.record.desktop_sid -cne $old.record.desktop_sid -or (ProfileDigest) -cne $profile -or (ServiceState) -cne $services){throw 'EXE did not recover/update without changing user/profile/service state'}
if((Test-Path $tx) -or (Test-Path (Join-Path $old.root 'maintenance.json'))){throw 'EXE left a pending transaction/fence'}
$results.Add(@{test='embedded-exe-recovers-and-updates';result='PASS'})
RunLabProcess $SetupEXE $setupArgs 0 'same-package'
if((ProfileDigest) -cne $profile -or (ServiceState) -cne $services){throw 'Same-package setup changed user state'}
$results.Add(@{test='same-package-idempotent';result='PASS'})
Stop-IRVIOForMaintenance|Out-Null
$probe=Join-Path $Payload 'daemon\irviod.exe'
try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash|ConvertTo-Json|Set-Content "$Evidence\clean.json"}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
$results.Add(@{test='exe-update-native-clean';result='PASS'})
$results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\results.json"
Get-FileHash $SetupEXE,$PSCommandPath,(Join-Path $Payload 'payload-manifest.json')|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'DEVELOPMENT EXE UPDATE/RECOVERY PASS; alternate-admin UAC not exercised by this unattended gate'
