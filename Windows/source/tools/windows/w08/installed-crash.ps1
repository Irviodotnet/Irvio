# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$API,
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$BootstrapFile,
 [Parameter(Mandatory=$true)][string]$PhysicalSource,
 [Parameter(Mandatory=$true)][string]$DNSProbe,
 [Parameter(Mandatory=$true)][string]$TrafficPhaseFile,
 [string]$Country='NL',
 [switch]$DisposableVM
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if(-not $DisposableVM){throw 'Disposable VM with out-of-band management required'}
. (Join-Path $Scripts 'software-update.ps1')
$installed=Read-IRVIOInstallation
if((Get-Service IRVIO).Status -ne 'Stopped'){throw 'Stopped installed desktop required'}
foreach($f in @($API,$DNSProbe,$TrafficPhaseFile,$BootstrapFile)){
 if(-not(Test-Path -LiteralPath $f -PathType Leaf)){throw 'Required input missing'}
}
if(Test-Path $Evidence){throw 'New evidence required'}
New-UpdateDirectory $Evidence
$clock=[Diagnostics.Stopwatch]::StartNew()
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
function Phase([string]$Value){
 Set-Content -LiteralPath $TrafficPhaseFile -Value $Value -Encoding ascii
 @{elapsed_ms=$clock.ElapsedMilliseconds;phase=$Value}|ConvertTo-Json -Compress|Add-Content "$Evidence\traffic-phases.ndjson"
}
function Sample([string]$Stage){
 $value=API status|ConvertFrom-Json
 @{elapsed_ms=$clock.ElapsedMilliseconds;stage=$Stage;status=$value}|ConvertTo-Json -Depth 8 -Compress|Add-Content "$Evidence\states.ndjson"
 return $value
}
function WaitConnected([string]$Stage){
 $deadline=$clock.ElapsedMilliseconds+120000
 while((Sample $Stage).state -ne 'CONNECTION_STATE_CONNECTED'){
  if($clock.ElapsedMilliseconds -gt $deadline){throw 'Connection deadline'}
  Start-Sleep -Milliseconds 500
 }
}
$probeAddress=@(Resolve-DnsName 'speed.cloudflare.com' -Type A -DnsOnly|Where-Object IPAddress|Select-Object -ExpandProperty IPAddress)[0]
if(-not $probeAddress){throw 'No baseline probe address'}
function Probe([string]$Name,[bool]$Physical,[bool]$Expected){
 $args=@('--noproxy','*','--fail','--silent','--show-error','--connect-timeout','3','--max-time','8','--output','NUL','--write-out','%{size_download}')
 if($Physical){$args+=@('--interface',$PhysicalSource,'--resolve',"speed.cloudflare.com:443:$probeAddress")}
 $args+='https://speed.cloudflare.com/__down?bytes=65536'
 $ErrorActionPreference='Continue'
 $at=$clock.ElapsedMilliseconds
 $bytes=& curl.exe @args 2> "$Evidence\$Name-error.log"
 $code=$LASTEXITCODE
 @{elapsed_ms=$at;duration_ms=$clock.ElapsedMilliseconds-$at;exit_code=$code;bytes="$bytes";physical=$Physical}|ConvertTo-Json|Set-Content "$Evidence\$Name.json"
 if($Expected){if($code -or [uint64]$bytes -ne 65536){throw "Transfer failed: $Name"}}elseif($code -eq 0 -or [uint64]$bytes -ne 0){throw "Physical-source probe escaped: $Name"}
}
$baseline=@(Network)
ConvertTo-Json -InputObject $baseline|Set-Content "$Evidence\network-before.json"
$binary=Join-Path $installed.program 'daemon\irviod.exe'
Get-FileHash $binary,$PSCommandPath,$DNSProbe|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
& sc.exe qfailure IRVIO|Set-Content "$Evidence\failure-policy.txt"
if($LASTEXITCODE){throw 'Cannot read service recovery policy'}
Probe 'ordinary-before' $true $true
Start-Service IRVIO
(Get-Service IRVIO).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
if((Sample 'idle').state -ne 'CONNECTION_STATE_DISCONNECTED'){throw 'Initial idle required'}
API import-file $BootstrapFile|Out-Null
API connect $Country|Out-Null
WaitConnected 'initial-connect'
Probe 'connected-before' $false $true
Phase 'protected'
Probe 'physical-blocked-before' $true $false
$daemon=Get-Process -Id (Get-CimInstance Win32_Service -Filter "Name='IRVIO'").ProcessId
if($daemon.Path -ine $binary){throw 'Unexpected daemon process path'}
$boot=(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToFileTimeUtc()
$scm=Get-Process services
$faultAt=$clock.ElapsedMilliseconds
@{elapsed_ms=$faultAt;pid=$daemon.Id;created=$daemon.StartTime.ToFileTimeUtc();scm_pid=$scm.Id;scm_created=$scm.StartTime.ToFileTimeUtc();boot=$boot}|ConvertTo-Json|Set-Content "$Evidence\fault.json"
Stop-Process -Id $daemon.Id -Force
if(-not $daemon.WaitForExit(5000)){throw 'Old daemon remained alive'}
$deadline=$clock.ElapsedMilliseconds+180000
$restarted=$null
do{
 $service=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
 @{elapsed_ms=$clock.ElapsedMilliseconds;state=$service.State;pid=$service.ProcessId}|ConvertTo-Json -Compress|Add-Content "$Evidence\service-observation.ndjson"
 if($service.State -eq 'Running' -and $service.ProcessId -gt 0){
  $candidate=Get-Process -Id $service.ProcessId -ErrorAction SilentlyContinue
  if($candidate -and ($candidate.Id -ne $daemon.Id -or $candidate.StartTime -ne $daemon.StartTime)){
   $restarted=$candidate
   break
  }
 }
 if($clock.ElapsedMilliseconds -gt $deadline){throw 'SCM automatic restart deadline'}
 Start-Sleep -Milliseconds 100
}while($true)
$restartAt=$clock.ElapsedMilliseconds
do{
 try{
  $state=Sample 'automatic-recovery'
  if($state.state -eq 'CONNECTION_STATE_CONNECTED'){break}
 }catch{
  @{elapsed_ms=$clock.ElapsedMilliseconds;api_unavailable=$true}|ConvertTo-Json -Compress|Add-Content "$Evidence\service-observation.ndjson"
 }
 if($clock.ElapsedMilliseconds -gt $deadline){throw 'Automatic reconnect deadline; no new Connect sent'}
 Start-Sleep -Milliseconds 250
}while($true)
$connectedAt=$clock.ElapsedMilliseconds
Probe 'connected-after' $false $true
$trafficAt=$clock.ElapsedMilliseconds
$raw=& $DNSProbe 'speed.cloudflare.com.'
$code=$LASTEXITCODE
@{exit_code=$code;result=($raw|ConvertFrom-Json)}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\dns-uncached-after.json"
if($code){throw 'Uncached DNS failed after crash recovery'}
Probe 'physical-blocked-after' $true $false
$now=Get-Process -Id (Get-CimInstance Win32_Service -Filter "Name='IRVIO'").ProcessId
$currentSCM=Get-Process services
if($now.Id -ne $restarted.Id -or $now.StartTime -ne $restarted.StartTime){throw 'Additional daemon restart occurred'}
if((Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToFileTimeUtc() -ne $boot -or $currentSCM.Id -ne $scm.Id -or $currentSCM.StartTime -ne $scm.StartTime){throw 'Not a same-boot daemon crash'}
@{pid=$now.Id;created=$now.StartTime.ToFileTimeUtc();restart_ms=$restartAt-$faultAt;connected_ms=$connectedAt-$faultAt;traffic_ms=$trafficAt-$faultAt}|ConvertTo-Json|Set-Content "$Evidence\recovery.json"
Phase 'teardown'
API disconnect|Out-Null
if((Sample 'disconnected').state -ne 'CONNECTION_STATE_DISCONNECTED'){throw 'Disconnect did not return idle'}
API shutdown|Out-Null
(Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(60))
$delta=@(Compare-Object $baseline @(Network))
@{delta=$delta}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\network-after.json"
if($delta.Count){throw 'Network baseline mismatch'}
Probe 'ordinary-after' $true $true
Stop-IRVIOForMaintenance|Out-Null
try{Assert-IRVIOMaintenanceClean $binary (Get-FileHash $binary).Hash|ConvertTo-Json|Set-Content "$Evidence\native-clean.json"}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
Phase 'after'
@{result='PASS';same_boot=$true;scm_automatic_restart=$true;fresh_connect_after_fault=$false;return_to_connected_ms=$connectedAt-$faultAt;return_to_completed_user_probe_ms=$trafficAt-$faultAt;network_baseline_equal=$true;native_cleanup=$true;continuous_leak_capture=$false}|ConvertTo-Json|Set-Content "$Evidence\verdict.json"
Get-Content "$Evidence\verdict.json"
