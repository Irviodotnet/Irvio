# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$SetupEXE,
 [Parameter(Mandatory=$true)][string]$PreviousSetupEXE,
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$BootstrapFile,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [switch]$FreshOnly
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
New-IRVIOServerDirectory $Evidence
$old=$null
if(-not $FreshOnly){
 $old=Read-IRVIOInstallation
 foreach($name in @('IRVIO','IRVIO-Recovery')){if((Get-Service $name).Status -ne 'Stopped'){throw 'Stopped disposable desktop installation required'}}
}
$bundle=[IO.File]::ReadAllText($BootstrapFile).Trim()
Assert-IRVIOServerBootstrapText $bundle
$results=New-Object 'System.Collections.Generic.List[object]'
function RunSetup([string]$Path,[string]$Arguments,[int]$Expected,[string]$Name){
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName=$Path;$start.Arguments=$Arguments;$start.UseShellExecute=$false;$start.CreateNoWindow=$true
 $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $child=New-Object Diagnostics.Process;$child.StartInfo=$start
 try{
  if(-not $child.Start()){throw 'Setup process failed to start'}
  $stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync()
  if(-not $child.WaitForExit(180000)){throw 'Installer still running; do not restart or force-kill it'}
  [IO.File]::WriteAllText("$Evidence\$Name.log",$stdout.GetAwaiter().GetResult())
  [IO.File]::WriteAllText("$Evidence\$Name-error.log",$stderr.GetAwaiter().GetResult())
  if($child.ExitCode -ne $Expected){throw "Unexpected setup exit for ${Name}: $($child.ExitCode)"}
 }finally{$child.Dispose()}
}
$install='--internal-install-server '+$bundle+' --development-unattended'
if(-not $FreshOnly){
$recordHash=(Get-FileHash (Join-Path $old.root 'installation.json')).Hash
RunSetup $SetupEXE $install 1 'desktop-overwrite-refusal'
Read-IRVIOInstallation|Out-Null
if((Get-FileHash (Join-Path $old.root 'installation.json')).Hash -cne $recordHash){throw 'Server install changed desktop record'}
$results.Add(@{test='server-refuses-existing-desktop';result='PASS'})
RunSetup $PreviousSetupEXE ('--internal-uninstall-for '+$old.record.desktop_sid+' keep-profile --development-unattended') 0 'remove-disposable-desktop'
Read-IRVIOPreservedInstallation $old.root $old.record.desktop_sid|Out-Null
foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Old service remains'}}
foreach($path in @($old.program,$old.driver)){if(Test-Path $path){throw 'Old application remains'}}
# Preserve the entire disposable desktop state outside canonical paths; never export this directory.
Move-Item -LiteralPath $old.root -Destination (Join-Path $Evidence 'retained-desktop')
}
$beforeAdapters=@(Get-NetAdapter -IncludeHidden|Select-Object -ExpandProperty InterfaceGuid|Sort-Object)
$beforeRoutes=@(Get-NetRoute|ForEach-Object{"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"}|Sort-Object)
$beforeDNS=@(Get-DnsClientServerAddress|ForEach-Object{"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}|Sort-Object)
@{adapters=$beforeAdapters;routes=$beforeRoutes;dns=$beforeDNS}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\baseline.json"
try{
 RunSetup $SetupEXE $install 0 'install-server'
 $installed=Read-IRVIOServerInstallation
 $service=Get-UninstallSCMService 'IRVIO'
 if($service.State -ne 4 -or $service.Start -ne 2){throw 'Server is not running with automatic startup'}
 if(Test-Path (Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk')){throw 'Server installed a desktop shortcut'}
 $results.Add(@{test='actual-exe-headless-scm-start';result='PASS';files=$installed.files.Count})
 $tcp=@(Get-NetTCPConnection -State Listen -LocalPort 4000 -ErrorAction Stop|Where-Object OwningProcess -eq $service.ProcessId)
 $stun=@(Get-NetUDPEndpoint -LocalPort 3478 -ErrorAction Stop|Where-Object OwningProcess -eq $service.ProcessId)
 $awg=@(Get-NetUDPEndpoint -LocalPort 51820 -ErrorAction Stop|Where-Object OwningProcess -eq $service.ProcessId)
 if(-not $tcp.Count -or -not $stun.Count -or -not $awg.Count){throw 'Server fixed endpoints are not owned by daemon'}
 $results.Add(@{test='server-owns-tcp4000-stun3478-awg51820';result='PASS'})
 $caught=$false;try{Read-IRVIOInstallation|Out-Null}catch{$caught=$true}
 if(-not $caught){throw 'Desktop maintenance accepted server installation'}
 $results.Add(@{test='desktop-maintenance-refuses-server';result='PASS'})
 $beforePID=(Get-UninstallSCMService IRVIO).ProcessId
 RunSetup $SetupEXE $install 0 'server-package-idempotence'
 Read-IRVIOServerInstallation|Out-Null
 if((Get-UninstallSCMService IRVIO).ProcessId -ne $beforePID){throw 'Same-package setup restarted a running server'}
 $results.Add(@{test='same-server-package-preserves-running-process';result='PASS'})
 $identity=(Get-FileHash (Join-Path $installed.root 'profile\identity.key')).Hash
 Stop-Service IRVIO
 (Get-Service IRVIO).WaitForStatus([ServiceProcess.ServiceControllerStatus]::Stopped,[TimeSpan]::FromSeconds(60))
 Start-Service IRVIO
 (Get-Service IRVIO).WaitForStatus([ServiceProcess.ServiceControllerStatus]::Running,[TimeSpan]::FromSeconds(60))
 Read-IRVIOServerInstallation|Out-Null
 if((Get-FileHash (Join-Path $installed.root 'profile\identity.key')).Hash -cne $identity){throw 'Server restart changed identity'}
 $results.Add(@{test='server-restart-keeps-identity';result='PASS'})
}finally{
 $service=Get-UninstallSCMService 'IRVIO'
 if($null -ne $service){
  Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','disabled')
  if($service.State -eq 4){Invoke-IRVIOMaintenanceSC @('stop','IRVIO')}
  $watch=[Diagnostics.Stopwatch]::StartNew()
  do{
   $service=Get-UninstallSCMService 'IRVIO'
   if($service.State -eq 1){break}
   if($watch.Elapsed.TotalSeconds -ge 60){throw 'Server did not stop gracefully; retained for inspection'}
   Start-Sleep -Milliseconds 100
  }while($true)
 }
}
$afterAdapters=@(Get-NetAdapter -IncludeHidden|Select-Object -ExpandProperty InterfaceGuid|Sort-Object)
$afterRoutes=@(Get-NetRoute|ForEach-Object{"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"}|Sort-Object)
$afterDNS=@(Get-DnsClientServerAddress|ForEach-Object{"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}|Sort-Object)
$rules=@(Get-NetFirewallRule -PolicyStore ActiveStore|Where-Object {$_.Group -in @('IRVIO.Control.v1','IRVIO.Inbound.v1')})
$cleanup=@{adapters=@(Compare-Object $beforeAdapters $afterAdapters);routes=@(Compare-Object $beforeRoutes $afterRoutes);dns=@(Compare-Object $beforeDNS $afterDNS);rules=$rules.Count}
$cleanup|ConvertTo-Json -Depth 5|Set-Content "$Evidence\cleanup.json"
if($cleanup.adapters.Count -or $cleanup.routes.Count -or $cleanup.dns.Count -or $cleanup.rules){throw 'Native server left host residue'}
$results.Add(@{test='graceful-stop-restores-host-network';result='PASS'})
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'ACTUAL EXE SERVER START/STOP PASS; public M11/GeoIP/Exit and second-client readiness not implied'
