# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [ValidateSet('desktop','server')][string]$Role='desktop'
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'software-update.ps1')
if($Role -ceq 'server'){. (Join-Path $Scripts 'server-installation.ps1')}
$old=Read-IRVIOMaintenanceInstallation $Role
$ownerField=if($Role -ceq 'server'){'bootstrap_sha256'}else{'desktop_sid'}
foreach($name in @('IRVIO','IRVIO-Recovery')){
 if((Get-Service $name).Status -ne 'Stopped'){throw 'Stopped disposable services required'}
}
New-UpdateDirectory $Evidence
$tx=Join-Path $old.root 'software-update'
if(Test-Path $tx){throw 'Existing pending transaction'}
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
function Crash([string]$Script,[string]$Point,[int]$Expected,[string]$Log){
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName="$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
 $start.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+(Join-Path $Scripts $Script)+'" -Role '+$Role+' -Payload "'+$Payload+'" -ManifestSHA256 '+$ManifestSHA256+' -DevelopmentUnsigned -InjectCrashAfterStep '+$Point
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $process=New-Object Diagnostics.Process
 $process.StartInfo=$start
 try{
  if(-not $process.Start()){throw 'Child did not start'}
  $stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
  if(-not $process.WaitForExit(180000)){throw 'Lab child still running; inspect its PID and transaction before reentry'}
  [IO.File]::WriteAllText("$Evidence\$Log.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Evidence\$Log-error.log",$stderr.GetAwaiter().GetResult())
  if($process.ExitCode -ne $Expected){throw "Wrong child exit at ${Point}: $($process.ExitCode)"}
 }finally{$process.Dispose()}
}
$profile=ProfileDigest;$services=ServiceState
$results=New-Object 'System.Collections.Generic.List[object]'
foreach($step in @('geo-restored','program-restored','metadata-restored','rolled-back','modes-restored','archived')){
 Crash 'update-desktop.ps1' 'metadata' 99 "$step-update"
 $id=(Get-Content -Raw "$tx\transaction.json"|ConvertFrom-Json).id
 Crash 'recover-update.ps1' $step 98 "$step-recovery"
 $recovery=& (Join-Path $Scripts 'recover-update.ps1') -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned
 $expected=if($step -eq 'archived'){'none'}else{'rolled-back'}
 if($recovery.phase -cne $expected){throw 'Unexpected recovery result'}
 $current=Read-IRVIOMaintenanceInstallation $Role
 if($current.record.manifest_sha256 -cne $old.record.manifest_sha256 -or $current.record.$ownerField -cne $old.record.$ownerField -or (ProfileDigest) -cne $profile -or (ServiceState) -cne $services){throw 'Recovery reentry changed profile, installation or service modes'}
 if((Test-Path $tx) -or (Test-Path (Join-Path $old.root 'maintenance.json'))){throw 'Pending transaction/fence remains'}
 $archive=Join-Path $old.root "update-history\$id\transaction.json"
 if((Get-Content -Raw $archive|ConvertFrom-Json).phase -cne 'rolled-back'){throw 'Terminal evidence missing'}
 $results.Add(@{test="recovery-crash-$step";result='PASS';archive=$archive})
 $results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\progress.json"
}
Stop-IRVIOForMaintenance -Role $Role|Out-Null
$probe=Join-Path $Payload 'daemon\irviod.exe'
try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash -Role $Role|ConvertTo-Json|Set-Content "$Evidence\clean.json"}finally{Restore-IRVIOMaintenanceStartModes -Role $Role|Out-Null}
$results.Add(@{test='reentry-native-clean';result='PASS'})
$results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\results.json"
Get-FileHash (Join-Path $Scripts 'recover-update.ps1'),$PSCommandPath,(Join-Path $Payload 'payload-manifest.json')|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'RECOVERY REENTRY PASS; no power-loss claim'
