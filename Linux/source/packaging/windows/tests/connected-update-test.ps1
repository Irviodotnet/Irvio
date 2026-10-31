# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$API,
 [Parameter(Mandatory=$true)][string]$BootstrapFile,
 [Parameter(Mandatory=$true)][string]$PhysicalSource,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [ValidatePattern('^[A-Z]{2}$')][string]$Country='NL',
 [switch]$DisposableVM
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if(-not $DisposableVM){throw 'Disposable Windows VM required'}
. "$Scripts\software-update.ps1"
$old=Read-IRVIOInstallation
if((Get-Service IRVIO).Status -ne 'Stopped'){throw 'Stopped disposable desktop required'}
if($old.record.manifest_sha256 -ieq $ManifestSHA256){throw 'Distinct update package required'}
Read-IRVIOPayload $Payload $ManifestSHA256 $true | Out-Null
New-UpdateDirectory $Evidence
$pipe='\\.\pipe\IRVIO.Control'
$clock=[Diagnostics.Stopwatch]::StartNew()
$results=New-Object 'System.Collections.Generic.List[object]'
function Pass([string]$Name){$results.Add(@{test=$Name;result='PASS';elapsed_ms=$clock.ElapsedMilliseconds});$results|ConvertTo-Json|Set-Content "$Evidence\results.json"}
function NetworkState {
 return @(
  Get-NetRoute|ForEach-Object{"route|$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$([uint32]$_.Protocol)"}
  Get-DnsClientServerAddress|ForEach-Object{"dns|$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}
  Get-NetAdapter -IncludeHidden|ForEach-Object{"adapter|$($_.InterfaceGuid)"}
 )|Sort-Object
}
function CallAPI([string]$Command,[string]$Argument='') {
 $ErrorActionPreference='Continue'
 if($Argument){$raw=& $API $Command $pipe $Argument 2> "$Evidence\api-error.txt"}else{$raw=& $API $Command $pipe 2> "$Evidence\api-error.txt"}
 if($LASTEXITCODE){throw "API command failed: $Command; inspect protected evidence"}
 return $raw
}
function AwaitState([string]$Wanted){
 $deadline=$clock.ElapsedMilliseconds+120000
 do{
  $status=CallAPI status|ConvertFrom-Json
  @{elapsed_ms=$clock.ElapsedMilliseconds;state=$status.state;country=$status.country;contribution=$status.contribution}|ConvertTo-Json -Compress|Add-Content "$Evidence\states.ndjson"
  if($status.state -eq $Wanted){return $status}
  if($clock.ElapsedMilliseconds -gt $deadline){throw "State timeout: $Wanted"}
  Start-Sleep -Milliseconds 500
 }while($true)
}
function StartIdle {
 Start-Service IRVIO
 (Get-Service IRVIO).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
 AwaitState 'CONNECTION_STATE_DISCONNECTED'|Out-Null
}
function Probe([string]$Name,[bool]$Physical,[bool]$ExpectSuccess){
 $args=@('--noproxy','*','--fail','--silent','--show-error','--connect-timeout','5','--max-time','15','--output','NUL','--write-out','%{size_download}')
 if($Physical){$args+=@('--interface',$PhysicalSource)}
 $args+='https://speed.cloudflare.com/__down?bytes=65536'
 $ErrorActionPreference='Continue'
 $size=& curl.exe @args 2> "$Evidence\$Name-error.log"
 $code=$LASTEXITCODE
 @{exit_code=$code;bytes="$size";physical=$Physical}|ConvertTo-Json|Set-Content "$Evidence\$Name.json"
 if($ExpectSuccess){if($code -or [uint64]$size -ne 65536){throw "User traffic probe failed: $Name"}}elseif($code -eq 0 -or [uint64]$size -ne 0){throw 'Physical user traffic escaped protection'}
}
function AssertNetwork([string]$Name){
 $delta=@(Compare-Object $baseline @(NetworkState))
 @{delta=$delta}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\$Name.json"
 if($delta.Count){throw 'Host routes/DNS/adapters differ from baseline'}
}
function AssertClean([string]$Name){
 Stop-IRVIOForMaintenance|Out-Null
 try{Assert-IRVIOMaintenanceClean "$Payload\daemon\irviod.exe" (Get-FileHash "$Payload\daemon\irviod.exe").Hash|ConvertTo-Json|Set-Content "$Evidence\$Name.json"}
 finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
}
$baseline=@(NetworkState)
$baseline|ConvertTo-Json|Set-Content "$Evidence\network-before.json"
Probe 'ordinary-before' $true $true
StartIdle
CallAPI import-file $BootstrapFile|Out-Null
$bootstrap=CallAPI bootstrap|ConvertFrom-Json
if($bootstrap.configured -lt 1){throw 'Bootstrap import failed'}
CallAPI connect $Country|Out-Null
$connected=AwaitState 'CONNECTION_STATE_CONNECTED'
if($connected.country -ne $Country -or -not $connected.contribution){throw 'Global CLIENT+EXIT not established'}
Probe 'connected-before' $false $true
Probe 'physical-blocked-before' $true $false
Pass 'old-client-connected-with-user-traffic-and-physical-probe-blocked'
$identity=(Get-FileHash "$($old.root)\profile\identity.key").Hash
$awg=(Get-FileHash "$($old.root)\profile\awg-identity").Hash
$started=$clock.ElapsedMilliseconds
& "$Scripts\update-desktop.ps1" -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned *> "$Evidence\update.log"
$new=Read-IRVIOInstallation
$svc=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
if($svc.State -ne 'Stopped' -or $svc.ProcessId -ne 0 -or $svc.ExitCode -ne 0 -or $svc.StartMode -ne 'Manual'){throw 'Update did not leave clean stopped demand-start desktop'}
if($new.record.manifest_sha256 -ine $ManifestSHA256 -or $new.record.desktop_sid -cne $old.record.desktop_sid){throw 'Wrong updated installation'}
if((Get-FileHash "$($old.root)\profile\identity.key").Hash -ne $identity -or (Get-FileHash "$($old.root)\profile\awg-identity").Hash -ne $awg){throw 'Update changed identity'}
@{duration_ms=$clock.ElapsedMilliseconds-$started}|ConvertTo-Json|Set-Content "$Evidence\update-timing.json"
AssertClean 'post-update-native-clean'
AssertNetwork 'post-update-network'
Probe 'ordinary-after' $true $true
Pass 'connected-update-graceful-stop-native-clean-profile-preserved'
& "$Scripts\recover-update.ps1" -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DevelopmentUnsigned *> "$Evidence\finalize.log"
StartIdle
$afterBootstrap=CallAPI bootstrap|ConvertFrom-Json
if($afterBootstrap.configured -ne $bootstrap.configured){throw 'Bootstrap profile changed'}
Start-Sleep -Seconds 3
$idle=CallAPI status|ConvertFrom-Json
if($idle.state -ne 'CONNECTION_STATE_DISCONNECTED'){throw 'Intentional maintenance resurrected connection'}
Pass 'updated-daemon-idle-no-autoconnect-bootstrap-preserved'
CallAPI connect $Country|Out-Null
AwaitState 'CONNECTION_STATE_CONNECTED'|Out-Null
Probe 'connected-after' $false $true
Probe 'physical-blocked-after' $true $false
Pass 'updated-daemon-explicit-reconnect-user-traffic'
CallAPI disconnect|Out-Null
AwaitState 'CONNECTION_STATE_DISCONNECTED'|Out-Null
AssertClean 'final-native-clean'
AssertNetwork 'final-network'
Probe 'final-ordinary' $true $true
Pass 'final-graceful-cleanup'
Get-FileHash $PSCommandPath,$API,"$Payload\payload-manifest.json","$Scripts\update-desktop.ps1","$Scripts\maintenance.ps1"|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
@{result='PASS';active_downstream_exercised=$false;continuous_transition_packet_capture=$false;old_manifest=$old.record.manifest_sha256;new_manifest=$ManifestSHA256;checks=$results.Count}|ConvertTo-Json|Set-Content "$Evidence\verdict.json"
Get-Content "$Evidence\verdict.json"
