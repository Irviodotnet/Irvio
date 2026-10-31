# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$SetupEXE,
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'uninstall.ps1')
New-UpdateDirectory $Evidence
$old=Read-IRVIOInstallation
$profile=Join-Path $old.root 'profile'
if(-not(Test-Path "$profile\identity.key")){throw 'A populated disposable profile is required; this test erases it'}
foreach($name in @('IRVIO','IRVIO-Recovery')){if((Get-Service $name).Status -ne 'Stopped'){throw 'Stopped disposable installation required'}}
function ProfileDigest {
 $parts=@(Get-ChildItem $profile -File -Recurse -Force|Sort-Object FullName|ForEach-Object {$_.FullName+'|'+(Get-FileHash $_.FullName).Hash})
 $hash=[Security.Cryptography.SHA256]::Create()
 try{return [BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes(($parts -join "`n")))).Replace('-','')}finally{$hash.Dispose()}
}
function RunSetup([string]$Arguments,[int]$Expected,[string]$Name){
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName=$SetupEXE;$start.Arguments=$Arguments
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $child=New-Object Diagnostics.Process;$child.StartInfo=$start
 try{
  if(-not $child.Start()){throw 'Setup process did not start'}
  $stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync()
  if(-not $child.WaitForExit(240000)){throw "EXE observation timeout; inspect running PID $($child.Id) and retained transaction before retry"}
  [IO.File]::WriteAllText("$Evidence\$Name.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Evidence\$Name-error.log",$stderr.GetAwaiter().GetResult())
  if($child.ExitCode -ne $Expected){throw "Unexpected EXE result for ${Name}: $($child.ExitCode)"}
 }finally{$child.Dispose()}
}
function AssertRemoved {
 foreach($path in @($old.program,$old.driver,(Join-Path $old.root 'geo'))){if(Test-Path $path){throw 'Application object remains'}}
 foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Service remains'}}
}
$before=ProfileDigest
$results=New-Object 'System.Collections.Generic.List[object]'
$sid=$old.record.desktop_sid
$install='--internal-install-for '+$sid+' --development-unattended'
$remove='--internal-uninstall-for '+$sid+' '
RunSetup '--internal-uninstall-for S-1-5-21-101-202-303-9999 remove-profile --development-unattended' 1 'wrong-owner'
Read-IRVIOInstallation|Out-Null
if((ProfileDigest) -cne $before -or (Test-Path (Join-Path $old.root 'uninstall.json'))){throw 'Wrong-user request changed profile/transaction'}
$results.Add(@{test='wrong-owner-refused-before-mutation';result='PASS'})
RunSetup ($remove+'keep-profile --development-unattended') 0 'keep'
AssertRemoved
if((ProfileDigest) -cne $before){throw 'EXE keep choice erased profile'}
$results.Add(@{test='exe-uninstall-keeps-profile';result='PASS'})
RunSetup $install 0 'reinstall-kept'
Read-IRVIOInstallation|Out-Null
if((ProfileDigest) -cne $before){throw 'EXE reinstall changed kept profile'}
$results.Add(@{test='exe-reinstalls-kept-profile';result='PASS'})
RunSetup ($remove+'remove-profile --development-unattended') 0 'erase'
AssertRemoved
Assert-IRVIOErasedProfile $old.root
$results.Add(@{test='exe-uninstall-erases-profile';result='PASS'})
RunSetup ($remove+'keep-profile --development-unattended') 1 'changed-choice'
RunSetup ($remove+'remove-profile --development-unattended') 0 'erase-repeat'
AssertRemoved
Assert-IRVIOErasedProfile $old.root
$results.Add(@{test='completed-erase-choice-fenced-and-idempotent';result='PASS'})
RunSetup $install 0 'reinstall-erased'
Read-IRVIOInstallation|Out-Null
Assert-IRVIOErasedProfile $old.root
$results.Add(@{test='exe-reinstall-keeps-erased-profile-empty';result='PASS'})
Stop-IRVIOForMaintenance|Out-Null
$probe=Join-Path $Payload 'daemon\irviod.exe'
try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash|ConvertTo-Json|Set-Content "$Evidence\clean.json"}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
$results.Add(@{test='reinstalled-native-clean';result='PASS'})
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
Get-FileHash $SetupEXE,$PSCommandPath|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'DEVELOPMENT EXE KEEP/ERASE UNINSTALL + REINSTALL PASS; interactive confirmations/UAC are a separate gate'
