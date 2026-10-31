# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][ValidateSet('Prepare','Observe')][string]$Mode,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$API,
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$PhysicalSource,
 [string]$BootstrapFile,
 [string]$Country='NL',
 [switch]$DisposableVM
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if(-not $DisposableVM){throw 'Disposable installed VM required'}
. (Join-Path $Scripts 'software-update.ps1')
$installed=Read-IRVIOInstallation
$pipe='\\.\pipe\IRVIO.Control'
function API([string]$Command,[string]$Argument=''){
 if($Argument){$raw=& $API $Command $pipe $Argument}else{$raw=& $API $Command $pipe}
 if($LASTEXITCODE){throw "API failed: $Command"}
 return $raw
}
function Network {
 @(
  Get-NetRoute|ForEach-Object{"route|$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$([uint32]$_.Protocol)"}
  Get-DnsClientServerAddress|ForEach-Object{"dns|$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}
  Get-NetAdapter -IncludeHidden|ForEach-Object{"adapter|$($_.InterfaceGuid)"}
 )|Sort-Object
}
function Egress([bool]$Physical){
 $args=@('--noproxy','*','--fail','--silent','--show-error','--connect-timeout','5','--max-time','15')
 if($Physical){$args+=@('--interface',$PhysicalSource)}
 $args+='https://api.ipify.org'
 $result=& curl.exe @args
 if($LASTEXITCODE){throw 'Egress probe failed'}
 $parsed=$null
 if(-not [Net.IPAddress]::TryParse($result.Trim(),[ref]$parsed)){throw 'Invalid egress response'}
 return $result.Trim()
}
if($Mode -eq 'Prepare'){
 if(Test-Path $Evidence){throw 'New evidence required'}
 if((Get-Service IRVIO).Status -ne 'Stopped'){throw 'Stopped installation required'}
 New-UpdateDirectory $Evidence
 ConvertTo-Json -InputObject @(Network)|Set-Content "$Evidence\network-before.json"
 $physical=Egress $true
 Start-Service IRVIO
 (Get-Service IRVIO).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
 if((API status|ConvertFrom-Json).state -ne 'CONNECTION_STATE_DISCONNECTED'){throw 'Initial idle required'}
 API import-file $BootstrapFile|Out-Null
 API connect $Country|Out-Null
 $clock=[Diagnostics.Stopwatch]::StartNew()
 do{
  $state=API status|ConvertFrom-Json
  $state|ConvertTo-Json -Compress -Depth 8|Add-Content "$Evidence\connect.ndjson"
  if($state.state -eq 'CONNECTION_STATE_CONNECTED'){break}
  if($clock.Elapsed.TotalSeconds -gt 120){throw 'Connect deadline; preserve state'}
  Start-Sleep -Milliseconds 500
 }while($true)
 $upstream=Egress $false
 if($upstream -eq $physical -or -not $state.contribution){throw 'Distinct upstream and contributor required'}
 @{physical=$physical;upstream=$upstream;connected_since=$state.connection.connected_since_unix_ms}|ConvertTo-Json|Set-Content "$Evidence\prepared.json"
 Get-FileHash "$($installed.program)\daemon\irviod.exe",$PSCommandPath|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
 'Prepared CLIENT+EXIT with distinct physical/upstream egress'
 return
}
$before=Get-Content "$Evidence\prepared.json" -Raw|ConvertFrom-Json
$clock=[Diagnostics.Stopwatch]::StartNew()
$seenFive=$false
do{
 $state=API status|ConvertFrom-Json
 $traffic=API traffic|ConvertFrom-Json
 $egress=Egress $false
 @{elapsed_ms=$clock.ElapsedMilliseconds;state=$state;traffic=$traffic;egress=$egress}|ConvertTo-Json -Depth 8 -Compress|Add-Content "$Evidence\samples.ndjson"
 if($state.state -ne 'CONNECTION_STATE_CONNECTED' -or -not $state.contribution -or $state.connection.connected_since_unix_ms -ne $before.connected_since -or $egress -ne $before.upstream){throw 'Own session changed during downstream traffic'}
 if($traffic.clients -eq 5){$seenFive=$true}
 if(Test-Path "$Evidence\clients-probed"){
  if(-not $seenFive -or $traffic.clients -ne 5){throw 'Five current clients required at Disconnect'}
  break
 }
 if($clock.Elapsed.TotalSeconds -gt 420){throw 'Downstream traffic deadline; preserve state'}
 Start-Sleep -Seconds 2
}while($true)
$disconnect=[Diagnostics.Stopwatch]::StartNew()
API disconnect|Out-Null
$disconnectMS=$disconnect.ElapsedMilliseconds
$state=API status|ConvertFrom-Json
$traffic=API traffic|ConvertFrom-Json
@{duration_ms=$disconnectMS;state=$state;traffic=$traffic}|ConvertTo-Json -Depth 8|Set-Content "$Evidence\disconnected.json"
$activeProperty=$traffic.PSObject.Properties['active']
if($state.state -ne 'CONNECTION_STATE_DISCONNECTED' -or $state.contribution -or $traffic.clients -ne 0 -or ($null -ne $activeProperty -and $activeProperty.Value)){throw 'Disconnect returned before idle/drain completed'}
$baseline=Get-Content "$Evidence\network-before.json" -Raw|ConvertFrom-Json
$delta=@(Compare-Object $baseline @(Network))
@{delta=$delta}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\network-after.json"
if($delta.Count){throw 'Network differs after Disconnect'}
if((Egress $true) -ne $before.physical){throw 'Ordinary physical egress not restored'}
API shutdown|Out-Null
(Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(60))
Stop-IRVIOForMaintenance|Out-Null
$probe=Join-Path $installed.program 'daemon\irviod.exe'
try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash|ConvertTo-Json|Set-Content "$Evidence\native-clean.json"}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
@{result='PASS';clients_at_disconnect=5;disconnect_ms=$disconnectMS;own_session_stable=$true;network_baseline_equal=$true;native_cleanup=$true;continuous_leak_capture=$false}|ConvertTo-Json|Set-Content "$Evidence\verdict.json"
Get-Content "$Evidence\verdict.json"
