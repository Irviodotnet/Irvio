# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$FixtureEXE,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'uninstall.ps1')
New-UpdateDirectory $Evidence
$old=Read-IRVIOInstallation
$profile=Join-Path $old.root 'profile'
$options=@{Payload=$Payload;ManifestSHA256=$ManifestSHA256;DevelopmentUnsigned=$true}
$uninstall=Join-Path $Scripts 'uninstall-desktop.ps1'
$results=New-Object 'System.Collections.Generic.List[object]'
function Populate([string]$Name){
 Stop-IRVIOForMaintenance|Out-Null
 try{
  $env:IRVIO_WINDOWS_MAINTENANCE_PROFILE_FIXTURE='1'
  & $FixtureEXE '-test.run=^TestWindowsNativeMaintenanceProfileFixture$' '-test.v' '-test.timeout=40s' 2>&1|Out-File "$Evidence\$Name.log"
  if($LASTEXITCODE){throw 'Native identity fixture failed'}
 }finally{Remove-Item Env:IRVIO_WINDOWS_MAINTENANCE_PROFILE_FIXTURE -ErrorAction SilentlyContinue;Restore-IRVIOMaintenanceStartModes|Out-Null}
}
Populate 'before'
$identity=(Get-FileHash "$profile\identity.key").Hash
$start=New-Object Diagnostics.ProcessStartInfo
$start.FileName="$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
$start.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$uninstall+'" -Payload "'+$Payload+'" -ManifestSHA256 '+$ManifestSHA256+' -DevelopmentUnsigned -RemoveProfile -InjectCrashAfterStep removing'
$start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
$child=New-Object Diagnostics.Process;$child.StartInfo=$start
try{
 if(-not $child.Start()){throw 'Uninstall did not start'}
 $stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync()
 if(-not $child.WaitForExit(180000)){$child.Kill();throw 'Uninstall timed out; preserve journal'}
 [IO.File]::WriteAllText("$Evidence\interrupted.log",$stdout.GetAwaiter().GetResult())
 [IO.File]::WriteAllText("$Evidence\interrupted-error.log",$stderr.GetAwaiter().GetResult())
 if($child.ExitCode -ne 97){throw "Did not reach profile-erased checkpoint: $($child.ExitCode)"}
}finally{$child.Dispose()}
$journal=Read-UninstallJournal (Join-Path $old.root 'uninstall.json')
if($journal.retain_profile -or $journal.phase -cne 'removing'){throw 'Explicit erase choice not persisted'}
Assert-IRVIOErasedProfile $old.root
Read-IRVIOInstallation|Out-Null
$results.Add(@{test='profile-erased-before-application-files-crash';result='PASS'})
$caught=$null
try{& $uninstall @options|Out-Null}catch{$caught=$_.Exception.Message}
if(-not $caught -or $caught -notmatch 'profile choice differs'){throw 'Resume accepted changed erase choice'}
$results.Add(@{test='changed-profile-choice-refused';result='PASS'})
& $uninstall @options -RemoveProfile|Out-File "$Evidence\resume.log"
& $uninstall @options -RemoveProfile|Out-File "$Evidence\repeat.log"
Assert-IRVIOErasedProfile $old.root
foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Service remains after erase uninstall'}}
foreach($path in @($old.program,$old.driver,(Join-Path $old.root 'geo'))){if(Test-Path $path){throw 'Application files remain'}}
$results.Add(@{test='erase-uninstall-resume-idempotence';result='PASS'})
& (Join-Path $Scripts 'install-or-update.ps1') @options -DesktopSID $old.record.desktop_sid|Out-File "$Evidence\reinstall.log"
$current=Read-IRVIOInstallation
if($current.record.manifest_sha256 -ine $ManifestSHA256){throw 'Reinstalled image differs from candidate'}
Assert-IRVIOErasedProfile $old.root
$results.Add(@{test='reinstall-does-not-resurrect-profile';result='PASS'})
Populate 'after'
if((Get-FileHash "$profile\identity.key").Hash -ceq $identity){throw 'Old identity returned after erase/reinstall'}
$results.Add(@{test='new-profile-gets-new-identity';result='PASS'})
Stop-IRVIOForMaintenance|Out-Null
try{
 $probe=Join-Path $Payload 'daemon\irviod.exe'
 Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash|ConvertTo-Json|Set-Content "$Evidence\clean.json"
}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
$results.Add(@{test='reinstalled-native-clean';result='PASS'})
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
Get-FileHash $uninstall,(Join-Path $Scripts 'maintenance.ps1'),(Join-Path $Scripts 'installation.ps1'),$PSCommandPath|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'PROFILE-ERASING UNINSTALL/REINSTALL PASS; original disposable identity erased'
