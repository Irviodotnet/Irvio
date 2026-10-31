# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$SetupA,[Parameter(Mandatory=$true)][string]$SetupB,[Parameter(Mandatory=$true)][string]$PayloadA,[Parameter(Mandatory=$true)][string]$PayloadB,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
$old=Read-IRVIOServerInstallation
if((Get-Service IRVIO).Status -ne 'Stopped' -or (Get-UninstallSCMService IRVIO).Start -ne 4){throw 'Stopped disabled disposable server required'}
New-IRVIOServerDirectory $Evidence
$bootstrap=[IO.File]::ReadAllText("$($old.root)\server-bootstrap.txt")
$identity=(Get-FileHash "$($old.root)\profile\identity.key").Hash
$hashA=(Get-FileHash "$PayloadA\payload-manifest.json").Hash
$hashB=(Get-FileHash "$PayloadB\payload-manifest.json").Hash
if($hashA -eq $hashB){throw 'Distinct packages required'}
$results=New-Object 'System.Collections.Generic.List[object]'
function RunSetup([string]$Exe,[string]$Name){
 $p=New-Object Diagnostics.Process
 $p.StartInfo=New-Object Diagnostics.ProcessStartInfo
 $p.StartInfo.FileName=$Exe
 $p.StartInfo.Arguments='--internal-install-server '+$bootstrap+' --development-unattended'
 $p.StartInfo.UseShellExecute=$false;$p.StartInfo.CreateNoWindow=$true
 $p.StartInfo.RedirectStandardOutput=$true;$p.StartInfo.RedirectStandardError=$true
 try{
  if(-not $p.Start()){throw 'Setup did not start'}
  $stdout=$p.StandardOutput.ReadToEndAsync();$stderr=$p.StandardError.ReadToEndAsync()
  if(-not $p.WaitForExit(240000)){throw "Setup remains running with PID $($p.Id); inspect before retry"}
  [IO.File]::WriteAllText("$Evidence\$Name.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Evidence\$Name-error.log",$stderr.GetAwaiter().GetResult())
  if($p.ExitCode -ne 0){throw "Setup failed at $Name with exit $($p.ExitCode)"}
 }finally{$p.Dispose()}
 Read-IRVIOServerInstallation|Out-Null
 if((Get-FileHash "$($old.root)\profile\identity.key").Hash -ne $identity -or [IO.File]::ReadAllText("$($old.root)\server-bootstrap.txt") -cne $bootstrap){throw 'Setup changed identity/bootstrap'}
 if((Test-Path "$($old.root)\software-update") -or (Test-Path "$($old.root)\maintenance.json")){throw 'Setup left unfinished update'}
}
RunSetup $SetupB 'update-disabled'
if((Read-IRVIOServerInstallation).record.manifest_sha256 -ine $hashB -or (Get-Service IRVIO).Status -ne 'Stopped' -or (Get-UninstallSCMService IRVIO).Start -ne 4){throw 'Disabled server startup policy changed'}
$results.Add(@{test='actual-exe-update-disabled-keeps-policy';result='PASS'})
RunSetup $SetupB 'repeat-disabled'
if((Get-Service IRVIO).Status -ne 'Stopped'){throw 'Same-package setup activated disabled server'}
$results.Add(@{test='same-package-disabled-idempotent';result='PASS'})
Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','demand')
RunSetup $SetupB 'repeat-manual'
if((Get-Service IRVIO).Status -ne 'Stopped' -or (Get-UninstallSCMService IRVIO).Start -ne 3){throw 'Manual server policy changed'}
$results.Add(@{test='manual-start-mode-retained';result='PASS'})
Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','auto')
RunSetup $SetupB 'start-auto'
if((Get-Service IRVIO).Status -ne 'Running'){throw 'Automatic server not started'}
$results.Add(@{test='same-package-auto-starts-stopped-server';result='PASS'})
$oldPID=(Get-UninstallSCMService IRVIO).ProcessId
RunSetup $SetupB 'repeat-running'
if((Get-UninstallSCMService IRVIO).ProcessId -ne $oldPID){throw 'Idempotent setup unnecessarily restarted server'}
$results.Add(@{test='same-package-running-keeps-process';result='PASS'})
RunSetup $SetupA 'update-running'
if((Read-IRVIOServerInstallation).record.manifest_sha256 -ine $hashA -or (Get-Service IRVIO).Status -ne 'Running' -or (Get-UninstallSCMService IRVIO).Start -ne 2){throw 'Automatic server update did not restart updated service'}
$results.Add(@{test='actual-exe-updates-running-auto-server';result='PASS'})
$before=(Get-FileHash "$($old.root)\installation.json").Hash
$caught=$null
try{& "$Scripts\install-or-update-server.ps1" -Payload $PayloadB -ManifestSHA256 $hashB -Bootstrap 'irvio-bootstrap:v1:QUJD' -DevelopmentUnsigned|Out-Null}catch{$caught=$_.Exception.Message}
if(-not $caught -or $caught -notmatch 'existing bootstrap bundle' -or (Get-FileHash "$($old.root)\installation.json").Hash -ne $before){throw 'Foreign bootstrap update not refused before mutation'}
$results.Add(@{test='different-bootstrap-refused-before-update';result='PASS'})
Stop-IRVIOForMaintenance -Role server|Out-Null
Assert-IRVIOMaintenanceClean "$PayloadA\daemon\irviod.exe" (Get-FileHash "$PayloadA\daemon\irviod.exe").Hash -Role server|ConvertTo-Json|Set-Content "$Evidence\clean.json"
Restore-IRVIOMaintenanceStartModes -Role server|Out-Null
Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','disabled')
$results.Add(@{test='final-server-native-clean';result='PASS'})
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'ACTUAL EXE SERVER UPDATE/IDEMPOTENCE/START-POLICY PASS; interactive UAC and public Exit readiness are separate'
