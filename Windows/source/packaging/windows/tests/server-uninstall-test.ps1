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
. (Join-Path $Scripts 'server-installation.ps1')
New-IRVIOServerDirectory $Evidence
$old=Read-IRVIOServerInstallation
if($old.record.manifest_sha256 -ine $ManifestSHA256){throw 'Current installed payload required'}
if((Get-UninstallSCMService IRVIO).Start -ne 4 -or (Get-UninstallSCMService IRVIO-Recovery).Start -ne 3){throw 'Disposable disabled server/manual recovery required'}
foreach($name in @('IRVIO','IRVIO-Recovery')){if((Get-UninstallSCMService $name).State -ne 1){throw 'Stopped disposable server required'}}
if(Test-Path "$($old.root)\uninstall.json"){throw 'Prior uninstall requires inspection'}
$results=New-Object 'System.Collections.Generic.List[object]'
$uninstall=Join-Path $Scripts 'uninstall-desktop.ps1'
$probe=Join-Path $Payload 'daemon\irviod.exe'
$probeHash=(Get-FileHash $probe).Hash
$options=@{Payload=$Payload;ManifestSHA256=$ManifestSHA256;DevelopmentUnsigned=$true;Role='server'}
function ProfileDigest {
 $parts=@(Get-ChildItem "$($old.root)\profile" -File -Recurse -Force|Sort-Object FullName|ForEach-Object {$_.FullName+'|'+(Get-FileHash $_.FullName).Hash})
 if(-not $parts.Count){throw 'Populated server profile required'}
 $hash=[Security.Cryptography.SHA256]::Create()
 try{return [BitConverter]::ToString($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes(($parts -join "`n")))).Replace('-','')}finally{$hash.Dispose()}
}
function Check-Clean([string]$Name){
 Stop-IRVIOForMaintenance -Role server|Out-Null
 try{Assert-IRVIOMaintenanceClean $probe $probeHash -Role server|ConvertTo-Json|Set-Content "$Evidence\$Name.json"}finally{Restore-IRVIOMaintenanceStartModes -Role server|Out-Null}
}
function Copy-Backup([string]$Source,[string]$Target,[string]$Log){
 & robocopy.exe $Source $Target /E /COPYALL /DCOPY:DAT /XJ /R:0 /W:0 /NFL /NDL *> $Log
 if($LASTEXITCODE -ge 8){throw 'Protected lab backup failed; no uninstall performed'}
 Assert-IRVIOInstallerACL $Target $true
}
function Crash-Step([string]$Step,[bool]$Erase,[string]$Dir){
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName="$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
 $start.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$uninstall+'" -Payload "'+$Payload+'" -ManifestSHA256 '+$ManifestSHA256+' -DevelopmentUnsigned -Role server -InjectCrashAfterStep '+$Step
 if($Erase){$start.Arguments+=' -RemoveProfile'}
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $p=New-Object Diagnostics.Process;$p.StartInfo=$start
 try{
  if(-not $p.Start()){throw 'Uninstall child failed to start'}
  $stdout=$p.StandardOutput.ReadToEndAsync();$stderr=$p.StandardError.ReadToEndAsync()
  if(-not $p.WaitForExit(180000)){throw "Uninstall observation timeout; child PID $($p.Id) must be inspected, not restarted"}
  [IO.File]::WriteAllText("$Dir\$Step.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Dir\$Step-error.log",$stderr.GetAwaiter().GetResult())
  if($p.ExitCode -ne 97){throw "Uninstall did not reach ${Step}: $($p.ExitCode)"}
 }finally{$p.Dispose()}
}
Check-Clean 'baseline-clean'
$profile=ProfileDigest
$recordHash=(Get-FileHash "$($old.root)\installation.json").Hash
$bootstrapHash=(Get-FileHash "$($old.root)\server-bootstrap.txt").Hash
foreach($mode in @('keep','erase')){
 $erase=$mode -ceq 'erase'
 $dir=Join-Path $Evidence $mode;New-IRVIOServerDirectory $dir
 $backup=Join-Path $dir 'private-backup';New-IRVIOServerDirectory $backup
 # Contains private identities; never include backup or retained-state in exported evidence.
 Copy-Backup $old.program "$backup\program" "$dir\backup-program.log"
 Copy-Backup $old.driver "$backup\driver" "$dir\backup-driver.log"
 Copy-Backup $old.root "$backup\state" "$dir\backup-state.log"
 foreach($step in @('removing','first-file','files-removed','first-service')){
  Crash-Step $step $erase $dir
  $journal=Read-UninstallJournal "$($old.root)\uninstall.json" 'server'
  if($journal.retain_profile -eq $erase){throw 'Profile intent changed across interruption'}
  if($erase){Assert-IRVIOErasedProfile $old.root}elseif((ProfileDigest) -cne $profile){throw 'Retained profile changed'}
  if($step -ceq 'removing'){
   $caught=$null;try{& $uninstall @options -RemoveProfile:(-not $erase)|Out-Null}catch{$caught=$_.Exception.Message}
   if(-not $caught -or $caught -notmatch 'profile choice differs'){throw 'Changed profile intent accepted'}
   $results.Add(@{test="$mode-changed-profile-intent-refused";result='PASS'})
  }
  $results.Add(@{test="$mode-interrupted-$step";result='PASS'})
 }
 & $uninstall @options -RemoveProfile:$erase *> "$dir\resume.log"
 & $uninstall @options -RemoveProfile:$erase *> "$dir\repeat.log"
 $receipt=Read-UninstallJournal "$($old.root)\uninstalled.json" 'server'
 if($receipt.phase -cne 'completed'){throw 'Uninstall not completed'}
 foreach($path in @($old.program,$old.driver,"$($old.root)\geo")){if(Test-Path $path){throw 'Application files remain'}}
 foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Service remains'}}
 if($erase){Assert-IRVIOErasedProfile $old.root}elseif((ProfileDigest) -cne $profile){throw 'Retained profile changed on completion'}
 if((Get-FileHash "$($old.root)\server-bootstrap.txt").Hash -cne $bootstrapHash){throw 'Server bootstrap changed'}
 $results.Add(@{test="$mode-completion-idempotence";result='PASS'})
 # Restore the lab baseline, not a product reinstall; retain all transaction evidence.
 [IO.Directory]::Move($old.root,"$dir\private-retained-state")
 [IO.Directory]::Move("$backup\program",$old.program)
 [IO.Directory]::Move("$backup\driver",$old.driver)
 [IO.Directory]::Move("$backup\state",$old.root)
 if((Get-FileHash "$($old.root)\installation.json").Hash -cne $recordHash -or (ProfileDigest) -cne $profile){throw 'Lab restore changed baseline'}
 $command=Get-IRVIOServerCommand $old
 Invoke-IRVIOMaintenanceSC @('create','WinDivert','type=','kernel','start=','demand','binPath=',('"'+$old.driver+'\WinDivert64.sys"'))
 New-Service IRVIO-Recovery -BinaryPathName ('"'+$old.program+'\daemon\irviod.exe" --boot-recover') -StartupType Manual -DisplayName 'IRVIO boot recovery'|Out-Null
 New-Service IRVIO -BinaryPathName $command -StartupType Disabled -DisplayName 'IRVIO Server and Exit Node' -DependsOn WinDivert|Out-Null
 Invoke-IRVIOMaintenanceSC @('sdset','IRVIO','D:P(A;;GA;;;SY)(A;;GA;;;BA)')
 Invoke-IRVIOMaintenanceSC @('failure','IRVIO','reset=','86400','actions=','restart/1000/restart/3000')
 Invoke-IRVIOMaintenanceSC @('failureflag','IRVIO','1')
 Read-IRVIOServerInstallation|Out-Null
 Check-Clean "$mode-restored-clean"
 $results.Add(@{test="$mode-lab-restored-native-clean";result='PASS'})
 $results|ConvertTo-Json|Set-Content "$Evidence\results.json"
}
Get-FileHash $uninstall,(Join-Path $Scripts 'uninstall.ps1'),$PSCommandPath|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'SERVER UNINSTALL NATIVE PASS; lab baseline restored, not a product reinstall proof'
