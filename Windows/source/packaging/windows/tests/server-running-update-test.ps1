# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Payload,[Parameter(Mandatory=$true)][string]$ManifestSHA256,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
$old=Read-IRVIOServerInstallation
if((Get-Service IRVIO).Status -ne 'Stopped' -or (Test-Path "$($old.root)\software-update")){throw 'Stopped disposable server with no pending update required'}
New-IRVIOServerDirectory $Evidence
$identity=(Get-FileHash "$($old.root)\profile\identity.key").Hash
$bootstrap=(Get-FileHash "$($old.root)\server-bootstrap.txt").Hash
$dns=@(Get-DnsClientServerAddress|ForEach-Object{"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}|Sort-Object)
$routes=@(Get-NetRoute|ForEach-Object{"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"}|Sort-Object)
$results=New-Object 'System.Collections.Generic.List[object]'
function WaitServerSockets {
 $watch=[Diagnostics.Stopwatch]::StartNew()
 do{
  $service=Get-UninstallSCMService 'IRVIO'
  if($service.State -eq 1){throw 'Server stopped before socket readiness'}
  $tcp=@(Get-NetTCPConnection -State Listen -LocalPort 4000 -ErrorAction SilentlyContinue|Where-Object OwningProcess -eq $service.ProcessId)
  $stun=@(Get-NetUDPEndpoint -LocalPort 3478 -ErrorAction SilentlyContinue|Where-Object OwningProcess -eq $service.ProcessId)
  $awg=@(Get-NetUDPEndpoint -LocalPort 51820 -ErrorAction SilentlyContinue|Where-Object OwningProcess -eq $service.ProcessId)
  if($service.State -eq 4 -and $tcp.Count -and $stun.Count -and $awg.Count){return}
  if($watch.Elapsed.TotalSeconds -gt 45){throw 'Server fixed-socket readiness timeout'}
  Start-Sleep -Milliseconds 200
 }while($true)
}
Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','auto')
Start-Service IRVIO
WaitServerSockets
$results.Add(@{test='old-server-running-with-fixed-sockets';result='PASS'})
$options=@{Payload=$Payload;ManifestSHA256=$ManifestSHA256;DevelopmentUnsigned=$true;Role='server'}
& "$Scripts\update-desktop.ps1" @options
$updated=Read-IRVIOServerInstallation
if($updated.record.manifest_sha256 -ine $ManifestSHA256 -or (Get-Service IRVIO).Status -ne 'Stopped' -or (Get-UninstallSCMService IRVIO).Start -ne 2){throw 'Running-server update did not finish stopped with original auto mode'}
$results.Add(@{test='running-server-update-graceful-stop';result='PASS'})
& "$Scripts\recover-update.ps1" @options|ConvertTo-Json|Set-Content "$Evidence\finalized.json"
Start-Service IRVIO
WaitServerSockets
if((Get-FileHash "$($old.root)\profile\identity.key").Hash -ne $identity -or (Get-FileHash "$($old.root)\server-bootstrap.txt").Hash -ne $bootstrap){throw 'Server update changed identity/bootstrap'}
$results.Add(@{test='updated-server-start-fixed-sockets-identity-kept';result='PASS'})
Stop-IRVIOForMaintenance -Role server|Out-Null
$probe=Join-Path $Payload 'daemon\irviod.exe'
Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash -Role server|ConvertTo-Json|Set-Content "$Evidence\clean.json"
Restore-IRVIOMaintenanceStartModes -Role server|Out-Null
Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','disabled')
$afterDNS=@(Get-DnsClientServerAddress|ForEach-Object{"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}|Sort-Object)
$afterRoutes=@(Get-NetRoute|ForEach-Object{"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"}|Sort-Object)
if(@(Compare-Object $dns $afterDNS).Count -or @(Compare-Object $routes $afterRoutes).Count){throw 'Updated server left route/DNS residue'}
$results.Add(@{test='updated-server-native-clean-route-dns-baseline';result='PASS'})
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'RUNNING SERVER UPDATE/RESTART PASS; no downstream-session or public Exit readiness claim'
