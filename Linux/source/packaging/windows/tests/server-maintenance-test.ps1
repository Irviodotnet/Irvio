# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Probe,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
$installed=Read-IRVIOServerInstallation
if((Get-Service IRVIO).Status -ne 'Stopped'){throw 'Stopped disposable server required'}
New-IRVIOServerDirectory $Evidence
$results=New-Object 'System.Collections.Generic.List[object]'
function Pass([string]$Name){$results.Add(@{test=$Name;result='PASS'})}
function Reject([string]$Name,[scriptblock]$Action,[string]$Expected){
 $caught=$null;try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong rejection for ${Name}: $caught"}
 Pass $Name
}
$identity=(Get-FileHash "$($installed.root)\profile\identity.key").Hash
$recordHash=(Get-FileHash "$($installed.root)\installation.json").Hash
$dns=@(Get-DnsClientServerAddress|ForEach-Object{"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}|Sort-Object)
$routes=@(Get-NetRoute|ForEach-Object{"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"}|Sort-Object)
$hash=(Get-FileHash $Probe).Hash
Protect-UpdateFile $Probe
Reject 'default-desktop-refuses-server' {Stop-IRVIOForMaintenance} 'Unsupported installation record'
Reject 'native-probe-refuses-unfenced-server' {Invoke-IRVIOMaintenanceProbe $Probe $hash} 'cleanly stopped, disabled'
Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','auto')
Start-Service IRVIO
(Get-Service IRVIO).WaitForStatus([ServiceProcess.ServiceControllerStatus]::Running,[TimeSpan]::FromSeconds(60))
Reject 'native-probe-refuses-running-server' {Invoke-IRVIOMaintenanceProbe $Probe $hash} 'cleanly stopped, disabled'
$record=Stop-IRVIOForMaintenance -Role server
if(-not $record.services[0].was_running -or $record.services[0].start -ne 'auto' -or $record.services[1].start -ne 'demand'){throw 'Server start modes not preserved'}
Pass 'running-server-graceful-fence'
Reject 'duplicate-stop' {Stop-IRVIOForMaintenance -Role server} 'Pending maintenance'
$raw=$record|ConvertTo-Json -Depth 5
$bad=$raw|ConvertFrom-Json;$bad.bootstrap_sha256=('0'*64)
Reject 'wrong-bootstrap-record' {Assert-IRVIOMaintenanceRecord $bad $installed} 'does not match'
$bad=$raw|ConvertFrom-Json;$bad.role='desktop'
Reject 'wrong-role-record' {Assert-IRVIOMaintenanceRecord $bad $installed} 'does not match'
$bad=$raw|ConvertFrom-Json;$bad.services[0].name='foreign'
Reject 'foreign-service-record' {Assert-IRVIOMaintenanceRecord $bad $installed} 'Invalid maintenance service'
Assert-IRVIOMaintenanceClean $Probe $hash -Role server|ConvertTo-Json|Set-Content "$Evidence\clean.json"
Pass 'native-server-core-clean'
$lock=Open-IRVIOMaintenanceLock $installed
try{Reject 'concurrent-restore' {Restore-IRVIOMaintenanceStartModes -Role server} 'being used by another process'}finally{$lock.Dispose()}
Restore-IRVIOMaintenanceStartModes -Role server|Out-Null
if((Get-Service IRVIO).Status -ne 'Stopped' -or (Get-UninstallSCMService IRVIO).Start -ne 2 -or (Get-UninstallSCMService IRVIO-Recovery).Start -ne 3){throw 'Restoration changed service activity or lost start modes'}
Pass 'restore-modes-without-restarting-server'
$realSC=(Get-Item Function:Invoke-IRVIOMaintenanceSC).ScriptBlock
$script:scCalls=0
try{
 function Invoke-IRVIOMaintenanceSC([string[]]$Arguments){$script:scCalls++;if($script:scCalls -eq 2){throw 'Injected second service configuration failure'};& $realSC $Arguments}
 Reject 'partial-disable-failure' {Stop-IRVIOForMaintenance -Role server} 'Injected second service'
}finally{Set-Item Function:Invoke-IRVIOMaintenanceSC $realSC}
$pending=Read-IRVIOMaintenanceRecord $installed
if($pending.services[0].start -ne 'auto'){throw 'Partial failure lost original start mode'}
Restore-IRVIOMaintenanceStartModes -Role server|Out-Null
Pass 'partial-failure-restoration'
Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','disabled')
Read-IRVIOServerInstallation|Out-Null
if((Get-FileHash "$($installed.root)\installation.json").Hash -ne $recordHash -or (Get-FileHash "$($installed.root)\profile\identity.key").Hash -ne $identity){throw 'Maintenance changed installation or identity'}
Pass 'installation-and-identity-unchanged'
$afterDNS=@(Get-DnsClientServerAddress|ForEach-Object{"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}|Sort-Object)
$afterRoutes=@(Get-NetRoute|ForEach-Object{"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"}|Sort-Object)
if(@(Compare-Object $dns $afterDNS).Count -or @(Compare-Object $routes $afterRoutes).Count){throw 'Maintenance changed baseline routes/DNS'}
Pass 'host-network-restored'
$results|ConvertTo-Json|Set-Content "$Evidence\results.json"
'SERVER MAINTENANCE PASS; software update/uninstall and public readiness are separate gates'
