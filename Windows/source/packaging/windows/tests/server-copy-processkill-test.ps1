# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Payload,[Parameter(Mandatory=$true)][string]$SetupEXE,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
New-IRVIOServerDirectory $Evidence
$installed=Read-IRVIOServerInstallation
if((Get-UninstallSCMService IRVIO).State -ne 1 -or (Get-UninstallSCMService IRVIO).Start -ne 4){throw 'Stopped disabled disposable server required'}
$root=$installed.root
$identity=(Get-FileHash "$root\profile\identity.key").Hash
$bootstrapHash=(Get-FileHash "$root\server-bootstrap.txt").Hash
$hash=(Get-FileHash "$Payload\payload-manifest.json").Hash
& "$Scripts\uninstall-desktop.ps1" -Role server -Payload $Payload -ManifestSHA256 $hash -DevelopmentUnsigned *> "$Evidence\uninstall.log"
$body=@'
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$scripts='__SCRIPTS__';$payload='__PAYLOAD__';$evidence='__EVIDENCE__'
function Copy-Item {
 param([string]$LiteralPath,[string]$Destination,[switch]$Force)
 if($Destination.EndsWith('\daemon\irviod.exe.irvio-install-part',[StringComparison]::OrdinalIgnoreCase)){
  $source=[IO.File]::OpenRead($LiteralPath)
  try{
   $bytes=New-Object byte[] 64;$n=$source.Read($bytes,0,64)
   $target=[IO.File]::Open($Destination,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
   try{
    $target.Write($bytes,0,$n);$target.Flush($true)
    [IO.File]::WriteAllText("$evidence\kill-ready.txt",$Destination)
    Start-Sleep -Seconds 600
    throw 'Harness did not terminate the paused installer'
   }finally{$target.Dispose()}
  }finally{$source.Dispose()}
 }
 Microsoft.PowerShell.Management\Copy-Item -LiteralPath $LiteralPath -Destination $Destination -Force:$Force
}
$bundle=[IO.File]::ReadAllText('C:\ProgramData\IRVIO\server-bootstrap.txt')
& "$scripts\install-server.ps1" -Payload $payload -ManifestSHA256 (Get-FileHash "$payload\payload-manifest.json").Hash -Bootstrap $bundle -DevelopmentUnsigned
'@
$body=$body.Replace('__SCRIPTS__',$Scripts.Replace("'","''")).Replace('__PAYLOAD__',$Payload.Replace("'","''")).Replace('__EVIDENCE__',$Evidence.Replace("'","''"))
$start=New-Object Diagnostics.ProcessStartInfo
$start.FileName="$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
$start.Arguments='-NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($body))
$start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
$child=New-Object Diagnostics.Process;$child.StartInfo=$start
try{
 if(-not $child.Start()){throw 'Installer child did not start'}
 @{pid=$child.Id;purpose='disposable installer copy interruption'}|ConvertTo-Json|Set-Content "$Evidence\child.json"
 $stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync()
 $watch=[Diagnostics.Stopwatch]::StartNew()
 while(-not(Test-Path "$Evidence\kill-ready.txt")){
  if($child.HasExited){throw 'Installer exited before copy barrier'}
  if($watch.Elapsed.TotalSeconds -gt 90){throw "Installer PID $($child.Id) still active; inspect it, do not retry"}
  Start-Sleep -Milliseconds 100
 }
 $temporary=[IO.File]::ReadAllText("$Evidence\kill-ready.txt")
 if($temporary -cne ($installed.program+'\daemon\irviod.exe.irvio-install-part')){throw 'Wrong kill target fixture'}
 $child.Kill()
 if(-not $child.WaitForExit(10000)){throw 'Installer termination not confirmed'}
 [IO.File]::WriteAllText("$Evidence\killed-installer.log",$stdout.GetAwaiter().GetResult())
 [IO.File]::WriteAllText("$Evidence\killed-installer-error.log",$stderr.GetAwaiter().GetResult())
 if((Get-Item $temporary).Length -ne 64 -or (Test-Path ($installed.program+'\daemon\irviod.exe'))){throw 'Interrupted copy was published or fixture changed'}
 foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Premature service during partial copy'}}
 @{forced_termination=$true;exit_code=$child.ExitCode;partial_bytes=64;final_file_absent=$true;services_absent=$true}|ConvertTo-Json|Set-Content "$Evidence\interruption.json"
}finally{$child.Dispose()}
$start=New-Object Diagnostics.ProcessStartInfo
$start.FileName=$SetupEXE
$start.Arguments='--internal-install-server '+[IO.File]::ReadAllText("$root\server-bootstrap.txt")+' --development-unattended'
$start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
$child=New-Object Diagnostics.Process;$child.StartInfo=$start
try{
 if(-not $child.Start()){throw 'Resume EXE did not start'}
 $stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync()
 if(-not $child.WaitForExit(180000)){throw "Resume EXE PID $($child.Id) still active; inspect before retry"}
 [IO.File]::WriteAllText("$Evidence\resume.log",$stdout.GetAwaiter().GetResult())
 [IO.File]::WriteAllText("$Evidence\resume-error.log",$stderr.GetAwaiter().GetResult())
 if($child.ExitCode){throw "Resume EXE failed: $($child.ExitCode)"}
}finally{$child.Dispose()}
$installed=Read-IRVIOServerInstallation
if((Get-UninstallSCMService IRVIO).State -ne 4 -or (Get-UninstallSCMService IRVIO).Start -ne 2){throw 'Resumed server not running automatically'}
if((Get-FileHash "$root\profile\identity.key").Hash -cne $identity -or (Get-FileHash "$root\server-bootstrap.txt").Hash -cne $bootstrapHash){throw 'Resume changed identity/bootstrap'}
if((Test-Path "$root\server-copy.json") -or (Test-Path $temporary)){throw 'Copy transaction residue'}
Stop-IRVIOForMaintenance -Role server|Out-Null
try{Assert-IRVIOMaintenanceClean "$Payload\daemon\irviod.exe" (Get-FileHash "$Payload\daemon\irviod.exe").Hash -Role server|ConvertTo-Json|Set-Content "$Evidence\cleanup.json"}
finally{Restore-IRVIOMaintenanceStartModes -Role server|Out-Null}
Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','disabled')
@{test='forced-installer-termination-then-exe-resume';result='PASS';identity_retained=$true;bootstrap_retained=$true}|ConvertTo-Json|Set-Content "$Evidence\result.json"
Get-FileHash $SetupEXE,$PSCommandPath|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'SERVER COPY PROCESS-KILL RESUME PASS; not a power-loss test'
