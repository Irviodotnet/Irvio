# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$SetupEXE,[Parameter(Mandatory=$true)][string]$Payload,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
New-IRVIOServerDirectory $Evidence
$old=Read-IRVIOServerInstallation
if((Get-UninstallSCMService IRVIO).State -ne 1 -or (Get-UninstallSCMService IRVIO).Start -ne 4){throw 'Stopped disabled disposable server required; test erases its identity'}
$bootstrap=[IO.File]::ReadAllText("$($old.root)\server-bootstrap.txt")
$identity=(Get-FileHash "$($old.root)\profile\identity.key").Hash
$manifestHash=(Get-FileHash "$Payload\payload-manifest.json").Hash
$probe=Join-Path $Payload 'daemon\irviod.exe'
$results=New-Object 'System.Collections.Generic.List[object]'
function RunSetup([string]$Arguments,[int]$Expected,[string]$Name){
 $p=New-Object Diagnostics.Process
 $p.StartInfo=New-Object Diagnostics.ProcessStartInfo
 $p.StartInfo.FileName=$SetupEXE;$p.StartInfo.Arguments=$Arguments
 $p.StartInfo.UseShellExecute=$false;$p.StartInfo.CreateNoWindow=$true
 $p.StartInfo.RedirectStandardOutput=$true;$p.StartInfo.RedirectStandardError=$true
 try{
  if(-not $p.Start()){throw 'Setup did not start'}
  $stdout=$p.StandardOutput.ReadToEndAsync();$stderr=$p.StandardError.ReadToEndAsync()
  if(-not $p.WaitForExit(240000)){throw "Setup observation timeout; PID $($p.Id) must be inspected before retry"}
  [IO.File]::WriteAllText("$Evidence\$Name.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Evidence\$Name-error.log",$stderr.GetAwaiter().GetResult())
  if($p.ExitCode -ne $Expected){throw "Unexpected EXE exit for ${Name}: $($p.ExitCode)"}
 }finally{$p.Dispose()}
}
function Assert-Removed([bool]$Erase){
 foreach($path in @($old.program,$old.driver,"$($old.root)\geo")){if(Test-Path $path){throw 'Application files remain'}}
 foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Service remains'}}
 $receipt=Read-IRVIOPreservedServerInstallation $old.root $old.program $bootstrap
 if($receipt.retain_profile -eq $Erase){throw 'Recorded profile intent differs'}
 if($Erase){Assert-IRVIOErasedProfile $old.root}elseif((Get-FileHash "$($old.root)\profile\identity.key").Hash -cne $identity){throw 'Keep choice changed identity'}
}
function Assert-Running([bool]$NewIdentity){
 $installed=Read-IRVIOServerInstallation
 if($installed.record.manifest_sha256 -ine $manifestHash){throw 'Reinstalled manifest differs'}
 $watch=[Diagnostics.Stopwatch]::StartNew()
 do{
  $service=Get-UninstallSCMService IRVIO
  if($service.State -ne 4 -or $service.Start -ne 2){throw 'Reinstalled server not running with automatic startup'}
  $identityPath="$($old.root)\profile\identity.key"
  $tcp=@(Get-NetTCPConnection -State Listen -LocalPort 4000 -ErrorAction SilentlyContinue|Where-Object OwningProcess -eq $service.ProcessId)
  $stun=@(Get-NetUDPEndpoint -LocalPort 3478 -ErrorAction SilentlyContinue|Where-Object OwningProcess -eq $service.ProcessId)
  $awg=@(Get-NetUDPEndpoint -LocalPort 51820 -ErrorAction SilentlyContinue|Where-Object OwningProcess -eq $service.ProcessId)
  if((Test-Path $identityPath) -and $tcp.Count -and $stun.Count -and $awg.Count){break}
  if($watch.Elapsed.TotalSeconds -gt 60){throw 'Reinstalled server identity/fixed sockets not ready'}
  Start-Sleep -Milliseconds 200
 }while($true)
 $same=(Get-FileHash $identityPath).Hash -ceq $identity
 if($same -eq $NewIdentity){throw 'Reinstalled identity violates keep/erase choice'}
 if([IO.File]::ReadAllText("$($old.root)\server-bootstrap.txt") -cne $bootstrap){throw 'Reinstall changed deployment bootstrap'}
 foreach($path in @("$($old.root)\uninstalled.json","$($old.root)\uninstall-manifest.json","$($old.program)\ui","$($old.root)\desktop-user.sid")){if(Test-Path $path){throw 'Reinstall retained stale receipt or introduced desktop component'}}
}
function Stop-Clean([string]$Name){
 Stop-IRVIOForMaintenance -Role server|Out-Null
 try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash -Role server|ConvertTo-Json|Set-Content "$Evidence\$Name.json"}finally{Restore-IRVIOMaintenanceStartModes -Role server|Out-Null}
 Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','disabled')
}
$remove='--internal-uninstall-server '
$install='--internal-install-server '+$bootstrap+' --development-unattended'
RunSetup '--internal-uninstall-for S-1-5-21-101-202-303-9999 keep-profile --development-unattended' 1 'desktop-refused'
Read-IRVIOServerInstallation|Out-Null
if(Test-Path "$($old.root)\uninstall.json"){throw 'Desktop uninstall mutated server transaction'}
$results.Add(@{test='desktop-exe-uninstall-refuses-server';result='PASS'})
RunSetup ($remove+'keep-profile --development-unattended') 0 'keep'
Assert-Removed $false
RunSetup ($remove+'keep-profile --development-unattended') 0 'keep-repeat'
$results.Add(@{test='server-exe-keep-and-repeat';result='PASS'})
$caught=$null
try{& "$Scripts\install-or-update-server.ps1" -Payload $Payload -ManifestSHA256 $manifestHash -Bootstrap 'irvio-bootstrap:v1:QUJD' -DevelopmentUnsigned|Out-Null}catch{$caught=$_.Exception.Message}
if(-not $caught -or $caught -notmatch 'retained bootstrap bundle'){throw 'Different-bootstrap reinstall was not refused'}
Assert-Removed $false
$results.Add(@{test='reinstall-different-bootstrap-refused';result='PASS'})
RunSetup $install 0 'reinstall-kept'
Assert-Running $false
$results.Add(@{test='server-exe-reinstall-retains-identity-fixed-ports';result='PASS'})
Stop-Clean 'kept-clean'
RunSetup ($remove+'remove-profile --development-unattended') 0 'erase'
Assert-Removed $true
RunSetup ($remove+'keep-profile --development-unattended') 1 'changed-choice'
RunSetup ($remove+'remove-profile --development-unattended') 0 'erase-repeat'
Assert-Removed $true
$results.Add(@{test='server-exe-erase-intent-and-repeat';result='PASS'})
RunSetup $install 0 'reinstall-erased'
Assert-Running $true
$results.Add(@{test='server-exe-reinstall-creates-new-identity-fixed-ports';result='PASS'})
Stop-Clean 'erased-clean'
$results.Add(@{test='reinstalled-server-native-clean';result='PASS'})
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
Get-FileHash $SetupEXE,$PSCommandPath|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'ACTUAL SERVER EXE UNINSTALL/REINSTALL PASS; interactive UAC and power-loss remain separate gates'
