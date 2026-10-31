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
if((Get-CimInstance Win32_Service -Filter "Name='IRVIO'").State -ne 'Stopped'){throw 'Stopped disposable installation required'}
New-UpdateDirectory $Evidence
$tx=Join-Path $old.root 'software-update'
if(Test-Path $tx){throw 'Pending transaction must be recovered first'}
$update=Join-Path $Scripts 'update-desktop.ps1'
$recover=Join-Path $Scripts 'recover-update.ps1'
$results=New-Object 'System.Collections.Generic.List[object]'
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
$profile=ProfileDigest;$services=ServiceState
$updateLock=Open-UpdateLock $old.root
try{
 $caught=$null
 try{& $update -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch 'being used by another process'){throw 'Concurrent update was not rejected'}
 if(Test-Path $tx){throw 'Concurrent attempt created transaction'}
 $results.Add(@{test='concurrent-update-refused';result='PASS'})
}finally{$updateLock.Dispose()}
foreach($step in @('prepared','fenced','program-retired','program-swap','geo-retired','geo-swap','metadata','committed')){
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName="$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
 $start.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$update+'" -Role '+$Role+' -Payload "'+$Payload+'" -ManifestSHA256 '+$ManifestSHA256+' -DevelopmentUnsigned -InjectCrashAfterStep '+$step
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $process=New-Object Diagnostics.Process
 $process.StartInfo=$start
 try{
  if(-not $process.Start()){throw 'Update child did not start'}
  $stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
  if(-not $process.WaitForExit(180000)){throw 'Update child still running; inspect its PID and transaction before reentry'}
  [IO.File]::WriteAllText("$Evidence\$step.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Evidence\$step-error.log",$stderr.GetAwaiter().GetResult())
  if($process.ExitCode -ne 99){throw "Update child did not reach $step : $($process.ExitCode)"}
 }finally{$process.Dispose()}
 $journal=Get-Content -Raw "$tx\transaction.json"|ConvertFrom-Json
 $phase=if($step -eq 'prepared'){'prepared'}elseif($step -eq 'committed'){'committed'}else{'swapping'}
 if($journal.phase -cne $phase){throw 'Child did not leave expected durable phase'}
 if($step -ne 'prepared'){
  foreach($name in @('IRVIO','IRVIO-Recovery')){
   $s=Get-CimInstance Win32_Service -Filter "Name='$name'"
   if($s.State -ne 'Stopped' -or $s.StartMode -ne 'Disabled'){throw 'Interrupted update lost service fence'}
  }
 }
 if($step -eq 'program-retired' -and (Test-Path $old.program)){throw 'Retired program unexpectedly exists'}
 if($step -eq 'geo-retired' -and (Test-Path (Join-Path $old.root 'geo'))){throw 'Retired GeoIP unexpectedly exists'}
 if($step -eq 'metadata'){
  $original=[IO.File]::ReadAllBytes("$tx\transaction.json")
  $bad=Get-Content -Raw "$tx\transaction.json"|ConvertFrom-Json
  $bad.program_previous='C:\unrelated-update-target'
  Write-UpdateJSON "$tx\transaction.json" $bad
  try{
   $before=(Get-FileHash "$tx\transaction.json").Hash;$caught=$null
   try{& $recover -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned}catch{$caught=$_.Exception.Message}
   if(-not $caught -or $caught -notmatch 'canonical transaction layout' -or (Get-FileHash "$tx\transaction.json").Hash -ne $before){throw 'Tampered journal was not refused/preserved'}
   $results.Add(@{test='foreign-journal-path-refused-preserved';result='PASS'})
  }finally{[IO.File]::WriteAllBytes("$tx\transaction.json",$original)}
  [IO.File]::WriteAllText((Join-Path $old.root 'installation.json.new'),'interrupted metadata fragment fixture')
 }
 $recovered=& $recover -Role $Role -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned
 $expected=if($step -eq 'committed'){$ManifestSHA256.ToLowerInvariant()}else{$old.record.manifest_sha256}
 $current=Read-IRVIOMaintenanceInstallation $Role
 if($current.record.manifest_sha256 -cne $expected -or $current.record.$ownerField -cne $old.record.$ownerField -or (ProfileDigest) -cne $profile -or (ServiceState) -cne $services){throw 'Recovery did not preserve expected installation/profile/start modes'}
 if((Test-Path $tx) -or (Test-Path (Join-Path $old.root 'maintenance.json'))){throw 'Recovered transaction or fence still pending'}
 if(-not(Test-Path (Join-Path $recovered.archive 'transaction.json'))){throw 'Recovery evidence not archived'}
 if($step -eq 'metadata' -and @(Get-ChildItem $recovered.archive -Filter 'interrupted-*.json').Count -ne 1){throw 'Interrupted metadata fragment not preserved'}
 $results.Add(@{test="crash-$step-recovery";result='PASS';phase=$recovered.phase;archive=$recovered.archive})
 $results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\progress.json"
}
Stop-IRVIOForMaintenance -Role $Role|Out-Null
$probe=Join-Path $Payload 'daemon\irviod.exe'
try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash -Role $Role|ConvertTo-Json|Set-Content "$Evidence\clean.json"}finally{Restore-IRVIOMaintenanceStartModes -Role $Role|Out-Null}
$results.Add(@{test='recovered-installation-native-clean';result='PASS'})
$results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\results.json"
Get-FileHash $update,$recover,(Join-Path $Scripts 'software-update.ps1'),(Join-Path $Scripts 'installation.ps1'),$PSCommandPath|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'PROCESS INTERRUPTION RECOVERY PASS; not a power-loss or active-session gate'
