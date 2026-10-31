# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'uninstall.ps1')
$old=Read-IRVIOInstallation
if($old.record.manifest_sha256 -ine $ManifestSHA256){throw 'Use the current installed payload for this gate'}
foreach($name in @('IRVIO','IRVIO-Recovery')){if((Get-Service $name).Status -ne 'Stopped'){throw 'Stopped disposable installation required'}}
New-UpdateDirectory $Evidence
$options=@{Payload=$Payload;ManifestSHA256=$ManifestSHA256;DevelopmentUnsigned=$true}
$uninstall=Join-Path $Scripts 'uninstall-desktop.ps1'
$install=Join-Path $Scripts 'install-or-update.ps1'
$results=New-Object 'System.Collections.Generic.List[object]'
function ProfileDigest {
 $parts=@(Get-ChildItem (Join-Path $old.root 'profile') -File -Recurse -Force|Sort-Object FullName|ForEach-Object {$_.FullName+'|'+(Get-FileHash $_.FullName).Hash})
 $hash=[Security.Cryptography.SHA256]::Create()
 try{return [BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes(($parts -join "`n")))).Replace('-','')}finally{$hash.Dispose()}
}
function New-Sentinel([string]$Path){
 $f=[IO.File]::Open($Path,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
 try{$bytes=[Text.Encoding]::UTF8.GetBytes('Uninstall test sentinel');$f.Write($bytes,0,$bytes.Length)}finally{$f.Dispose()}
 return (Get-FileHash $Path).Hash
}
$profile=ProfileDigest
$unknown=Join-Path $old.program 'uninstall-unowned-test.txt'
$unknownHash=New-Sentinel $unknown
try{
 $caught=$null
 try{& $uninstall @options|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch 'Unowned file in uninstall target'){throw "Unexpected preflight: $caught"}
 Read-IRVIOInstallation|Out-Null
 if((Get-FileHash $unknown).Hash -cne $unknownHash -or (ProfileDigest) -cne $profile){throw 'Unknown file/profile changed on refusal'}
 $results.Add(@{test='unowned-file-refused-preserved';result='PASS'})
}finally{
 if((Get-FileHash $unknown).Hash -cne $unknownHash){throw 'Sentinel changed; not removing it'}
 Remove-Item -LiteralPath $unknown
}
$retained=Join-Path $old.root 'uninstall-retained-test.txt'
$retainedHash=New-Sentinel $retained
foreach($step in @('removing','first-file','files-removed','first-service')){
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName="$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
 $start.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$uninstall+'" -Payload "'+$Payload+'" -ManifestSHA256 '+$ManifestSHA256+' -DevelopmentUnsigned -InjectCrashAfterStep '+$step
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $p=New-Object Diagnostics.Process;$p.StartInfo=$start
 try{
  if(-not $p.Start()){throw 'Uninstall child did not start'}
  $stdout=$p.StandardOutput.ReadToEndAsync();$stderr=$p.StandardError.ReadToEndAsync()
  if(-not $p.WaitForExit(180000)){throw "Uninstall observation timeout; inspect running PID $($p.Id) before retry"}
  [IO.File]::WriteAllText("$Evidence\$step.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Evidence\$step-error.log",$stderr.GetAwaiter().GetResult())
  if($p.ExitCode -ne 97){throw "Uninstall did not reach ${step}: $($p.ExitCode)"}
 }finally{$p.Dispose()}
 & $uninstall @options|Out-File "$Evidence\$step-resume.log"
 foreach($path in @($old.program,$old.driver,(Join-Path $old.root 'geo'),(Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk'))){if(Test-Path $path){throw 'Application files remain after uninstall'}}
 foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if(Get-Service $name -ErrorAction SilentlyContinue){throw 'Service remains after uninstall'}}
 if((ProfileDigest) -cne $profile -or (Get-FileHash $retained).Hash -cne $retainedHash){throw 'Retained profile/unowned data changed'}
 & $uninstall @options|Out-File "$Evidence\$step-repeat.log"
 $wrong=(Get-LocalUser IrvioLab).SID.Value
 if($wrong -eq $old.record.desktop_sid){throw 'Distinct enabled test account required'}
 $caught=$null
 try{& $install @options -DesktopSID $wrong|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch 'another desktop user'){throw 'Reinstall did not reject a different identity'}
 & $install @options -DesktopSID $old.record.desktop_sid|Out-File "$Evidence\$step-reinstall.log"
 $current=Read-IRVIOInstallation
 if($current.record.manifest_sha256 -cne $old.record.manifest_sha256 -or $current.record.desktop_sid -cne $old.record.desktop_sid -or (ProfileDigest) -cne $profile -or (Get-FileHash $retained).Hash -cne $retainedHash){throw 'Reinstall did not retain original identity/profile/data'}
 foreach($name in @('IRVIO','IRVIO-Recovery')){if((Get-Service $name).Status -ne 'Stopped'){throw 'Reinstall started a daemon'}}
 $results.Add(@{test="uninstall-crash-$step-resume-reinstall";result='PASS'})
 $results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\progress.json"
}
if((Get-FileHash $retained).Hash -cne $retainedHash){throw 'Retained sentinel changed; not removing it'}
Remove-Item -LiteralPath $retained
Stop-IRVIOForMaintenance|Out-Null
$probe=Join-Path $Payload 'daemon\irviod.exe'
try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash|ConvertTo-Json|Set-Content "$Evidence\clean.json"}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
$results.Add(@{test='reinstalled-native-clean';result='PASS'})
$results|ConvertTo-Json -Depth 4|Set-Content "$Evidence\results.json"
Get-FileHash $uninstall,(Join-Path $Scripts 'uninstall.ps1'),(Join-Path $Scripts 'installation.ps1'),(Join-Path $Scripts 'install-desktop.ps1'),$PSCommandPath|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'RETAINED-PROFILE UNINSTALL/REINSTALL PASS; profile erasure and EXE uninstall UX remain separate gates'
