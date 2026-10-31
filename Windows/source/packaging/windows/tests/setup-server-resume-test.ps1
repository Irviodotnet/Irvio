# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$SetupEXE,[Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Payload,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
New-IRVIOServerDirectory $Evidence
$installed=Read-IRVIOServerInstallation
if((Get-UninstallSCMService IRVIO).State -ne 1 -or (Get-UninstallSCMService IRVIO).Start -ne 4){throw 'Stopped disabled disposable server required'}
$root=$installed.root
$bundle=[IO.File]::ReadAllText("$root\server-bootstrap.txt")
$identity=(Get-FileHash "$root\profile\identity.key").Hash
$bootstrapHash=(Get-FileHash "$root\server-bootstrap.txt").Hash
$hash=(Get-FileHash "$Payload\payload-manifest.json").Hash
$results=New-Object 'System.Collections.Generic.List[object]'
function RunSetup([string]$Name){
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName=$SetupEXE;$start.Arguments='--internal-install-server '+$bundle+' --development-unattended'
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true
 $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $child=New-Object Diagnostics.Process;$child.StartInfo=$start
 try{
  if(-not $child.Start()){throw 'Setup process did not start'}
  $stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync()
  if(-not $child.WaitForExit(180000)){throw "Setup PID $($child.Id) is still running; inspect it before retrying"}
  [IO.File]::WriteAllText("$Evidence\$Name.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Evidence\$Name-error.log",$stderr.GetAwaiter().GetResult())
  if($child.ExitCode){throw "Setup failed: $($child.ExitCode)"}
 }finally{$child.Dispose()}
}
foreach($phase in @('service','started')){
 & "$Scripts\uninstall-desktop.ps1" -Role server -Payload $Payload -ManifestSHA256 $hash -DevelopmentUnsigned *> "$Evidence\$phase-uninstall.log"
 $caught=$null
 try{& "$Scripts\install-server.ps1" -Payload $Payload -ManifestSHA256 $hash -Bootstrap $bundle -DevelopmentUnsigned -DevelopmentStopAfter $phase|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch "IRVIO development interruption: $phase"){throw "Expected interruption absent: $caught"}
 $before=Get-UninstallSCMService IRVIO
 RunSetup "$phase-resume"
 $installed=Read-IRVIOServerInstallation
 $after=Get-UninstallSCMService IRVIO
 if($after.State -ne 4 -or $after.Start -ne 2){throw 'EXE did not complete automatic server startup'}
 if($phase -ceq 'started' -and $before.ProcessId -ne $after.ProcessId){throw 'EXE restarted already-running daemon'}
 if((Get-FileHash "$root\profile\identity.key").Hash -cne $identity -or (Get-FileHash "$root\server-bootstrap.txt").Hash -cne $bootstrapHash){throw 'EXE changed retained identity/bootstrap'}
 RunSetup "$phase-repeat"
 if((Get-UninstallSCMService IRVIO).ProcessId -ne $after.ProcessId){throw 'Repeated EXE setup restarted daemon'}
 Stop-IRVIOForMaintenance -Role server|Out-Null
 try{Assert-IRVIOMaintenanceClean "$Payload\daemon\irviod.exe" (Get-FileHash "$Payload\daemon\irviod.exe").Hash -Role server|ConvertTo-Json|Set-Content "$Evidence\$phase-cleanup.json"}
 finally{Restore-IRVIOMaintenanceStartModes -Role server|Out-Null}
 Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','disabled')
 $results.Add(@{test="$phase-exe-resume-idempotence-identity-cleanup";result='PASS'})
 $results|ConvertTo-Json|Set-Content "$Evidence\results.json"
}
Get-FileHash $SetupEXE,$PSCommandPath|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'SERVER EXE PENDING SETUP RESUME PASS; development unattended child path, not interactive UAC acceptance'
