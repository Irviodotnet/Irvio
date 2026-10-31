# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][ValidateSet('Prepare','Verify')][string]$Mode,
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
if(-not $DisposableVM){throw 'Disposable VM required'}
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
function Traffic([string]$Name,[bool]$Physical,[bool]$Expected){
 $args=@('--noproxy','*','--fail','--silent','--show-error','--connect-timeout','5','--max-time','15','--output','NUL','--write-out','%{size_download}')
 if($Physical){$args+=@('--interface',$PhysicalSource)}
 $args+='https://speed.cloudflare.com/__down?bytes=65536'
 $ErrorActionPreference='Continue'
 $clock=[Diagnostics.Stopwatch]::StartNew()
 $bytes=& curl.exe @args 2> "$Evidence\$Name-error.log"
 $code=$LASTEXITCODE
 @{exit_code=$code;bytes="$bytes";elapsed_ms=$clock.ElapsedMilliseconds;physical=$Physical}|ConvertTo-Json|Set-Content "$Evidence\$Name.json"
 if($Expected){if($code -or [uint64]$bytes -ne 65536){throw "Transfer failed: $Name"}}elseif($code -eq 0 -or [uint64]$bytes -ne 0){throw "Physical WAN probe leaked: $Name"}
}
function WaitConnected([string]$Name){
 $clock=[Diagnostics.Stopwatch]::StartNew()
 do{
  $state=API status|ConvertFrom-Json
  @{elapsed_ms=$clock.ElapsedMilliseconds;state=$state}|ConvertTo-Json -Compress -Depth 8|Add-Content "$Evidence\$Name.ndjson"
  if($state.state -eq 'CONNECTION_STATE_CONNECTED'){return $clock.ElapsedMilliseconds}
  if($clock.Elapsed.TotalSeconds -gt 120){throw 'Connection deadline; preserve state'}
  Start-Sleep -Milliseconds 500
 }while($true)
}
if($Mode -eq 'Prepare'){
 if(Test-Path $Evidence){throw 'New evidence directory required'}
 if((Get-Service IRVIO).Status -ne 'Stopped'){throw 'Stopped installation required'}
 New-UpdateDirectory $Evidence
 $baseline=@(Network)
 ConvertTo-Json -InputObject $baseline|Set-Content "$Evidence\network-before.json"
 Traffic 'ordinary-before' $true $true
 Start-Service IRVIO
 (Get-Service IRVIO).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
 if((API status|ConvertFrom-Json).state -ne 'CONNECTION_STATE_DISCONNECTED'){throw 'Initial idle required'}
 API import-file $BootstrapFile|Out-Null
 API connect $Country|Out-Null
 $elapsed=WaitConnected 'connect'
 Traffic 'connected-before' $false $true
 Traffic 'physical-blocked-before' $true $false
 $scm=Get-Process services
 $svc=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
 $daemon=Get-Process -Id $svc.ProcessId
 @{scm_pid=$scm.Id;scm_created=$scm.StartTime.ToFileTimeUtc();daemon_pid=$daemon.Id;daemon_created=$daemon.StartTime.ToFileTimeUtc();connect_ms=$elapsed;kernel_boot_record=(Get-WinEvent -FilterHashtable @{LogName='System';ProviderName='Microsoft-Windows-Kernel-Boot';Id=27} -MaxEvents 1).RecordId}|ConvertTo-Json|Set-Content "$Evidence\prepared.json"
 Get-FileHash "$($installed.program)\daemon\irviod.exe",$PSCommandPath|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
 'CONNECTED and physical probe blocked; external coordinator may request hibernation'
 return
}
$before=Get-Content "$Evidence\prepared.json" -Raw|ConvertFrom-Json
$event=Get-WinEvent -FilterHashtable @{LogName='System';ProviderName='Microsoft-Windows-Kernel-Boot';Id=27} -MaxEvents 1
$event.ToXml()|Set-Content "$Evidence\boot-event.xml"
$xml=[xml]$event.ToXml()
$bootType=@($xml.Event.EventData.Data|Where-Object Name -eq 'BootType')[0].'#text'
if($event.RecordId -le $before.kernel_boot_record -or [int]$bootType -ne 2){throw 'Actual hibernate resume not proved; retain evidence'}
$scm=Get-Process services
$daemon=Get-Process -Id $before.daemon_pid
if($scm.Id -ne $before.scm_pid -or $scm.StartTime.ToFileTimeUtc() -ne $before.scm_created -or $daemon.StartTime.ToFileTimeUtc() -ne $before.daemon_created){throw 'SCM/daemon instance changed across hibernation'}
Traffic 'physical-blocked-resumed' $true $false
$elapsed=WaitConnected 'resume'
Traffic 'connected-after' $false $true
Traffic 'physical-blocked-after' $true $false
API disconnect|Out-Null
API shutdown|Out-Null
(Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(60))
$baseline=Get-Content "$Evidence\network-before.json" -Raw|ConvertFrom-Json
$delta=@(Compare-Object $baseline @(Network))
@{delta=$delta}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\network-after.json"
if($delta.Count){throw 'Network residue'}
Traffic 'ordinary-after' $true $true
. (Join-Path $Scripts 'maintenance.ps1')
Stop-IRVIOForMaintenance|Out-Null
$probe=Join-Path $installed.program 'daemon\irviod.exe'
try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash|ConvertTo-Json|Set-Content "$Evidence\native-clean.json"}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
@{result='PASS';boot_type=2;scm_preserved=$true;daemon_preserved=$true;resume_observation_to_connected_ms=$elapsed;fresh_connect_sent_after_resume=$false;native_cleanup=$true;network_baseline_equal=$true;continuous_leak_capture=$false}|ConvertTo-Json|Set-Content "$Evidence\verdict.json"
Get-Content "$Evidence\verdict.json"
