# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$API,
 [Parameter(Mandatory=$true)][string]$Invitation,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$PhysicalSource,
 [switch]$DNSForensic,
 [switch]$DisposableVM
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if(-not $DisposableVM){throw 'Disposable VM required'}
$svc=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
if($svc.State -ne 'Stopped' -or $svc.PathName -ne '"C:\Program Files\IRVIO-W08\irviod.exe"'){throw 'Stopped disposable W08 service required'}
if(Test-Path $Evidence){throw 'New private evidence path required'}
New-Item -ItemType Directory $Evidence|Out-Null
$acl=New-Object Security.AccessControl.DirectorySecurity
$acl.SetSecurityDescriptorSddlForm('O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)')
Set-Acl $Evidence $acl
$pipe='\\.\pipe\IRVIO.Control'
$clock=[Diagnostics.Stopwatch]::StartNew()
function Marker([string]$Name){
 @{event=$Name;elapsed_ms=$clock.ElapsedMilliseconds;utc_ms=[DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()}|ConvertTo-Json -Compress|Add-Content "$Evidence\timing.ndjson"
}
function DNSCache([string]$Name){
 if(-not $DNSForensic){return}
 Marker "$Name-start"
 $rows=@(Get-DnsClientCache|Where-Object {$_.Entry -like '*cloudflare*'}|Select-Object Entry,RecordName,RecordType,Status,TimeToLive,Data)
 ConvertTo-Json -InputObject $rows|Set-Content "$Evidence\$Name.json"
 Marker "$Name-end"
}
function API([string]$Command,[string]$Argument=''){
 $ErrorActionPreference='Continue'
 if($Argument){$raw=& $API $Command $pipe $Argument 2> "$Evidence\api-error.txt"}else{$raw=& $API $Command $pipe 2> "$Evidence\api-error.txt"}
 if($LASTEXITCODE){throw "API failed: $Command"}
 return $raw
}
function Network {
 return @(
  Get-NetRoute|ForEach-Object{"route|$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$([uint32]$_.Protocol)"}
  Get-DnsClientServerAddress|ForEach-Object{"dns|$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}
  Get-NetAdapter -IncludeHidden|ForEach-Object{"adapter|$($_.InterfaceGuid)"}
 )|Sort-Object
}
function Probe([string]$Name,[bool]$Physical,[bool]$Success){
 Marker "$Name-start"
 $args=@('--noproxy','*','--fail','--silent','--show-error','--connect-timeout','5','--max-time','15','--output','NUL','--write-out','%{size_download}')
 if($Physical){$args+=@('--interface',$PhysicalSource)}
 $args+='https://speed.cloudflare.com/__down?bytes=65536'
 $ErrorActionPreference='Continue'
 $bytes=& curl.exe @args 2> "$Evidence\$Name-error.log"
 $code=$LASTEXITCODE
 Marker "$Name-end"
 @{bytes="$bytes";exit_code=$code;physical=$Physical}|ConvertTo-Json|Set-Content "$Evidence\$Name.json"
 if($Success){if($code -or [uint64]$bytes -ne 65536){throw "Traffic failed: $Name"}}elseif($code -eq 0 -or [uint64]$bytes -ne 0){throw "Unexpected traffic success: $Name"}
}
$baseline=@(Network)
$baseline|ConvertTo-Json|Set-Content "$Evidence\network-before.json"
Probe 'ordinary-before' $true $true
Start-Service IRVIO
(Get-Service IRVIO).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
$initial=API private-summary|ConvertFrom-Json
if($initial.state -ne 'CONNECTION_STATE_DISCONNECTED'){throw 'Initial client not idle'}
$imported=API private-import-file $Invitation|ConvertFrom-Json
$target=$imported.target_peer_id
API private-enroll $target|Out-Null
API private-connect $target|Out-Null
$deadline=$clock.ElapsedMilliseconds+120000
do{
 $state=API private-summary|ConvertFrom-Json
 $state|ConvertTo-Json -Compress|Add-Content "$Evidence\states.ndjson"
 if($state.state -eq 'CONNECTION_STATE_CONNECTED'){break}
 if($clock.ElapsedMilliseconds -gt $deadline){throw 'Private connection timed out'}
 Start-Sleep -Milliseconds 500
}while($true)
if(-not $state.private_target_active){throw 'Private exact-target intent missing'}
Probe 'connected' $false $true
Probe 'physical-blocked-connected' $true $false
'ready'|Set-Content "$Evidence\client-ready"
$deadline=$clock.ElapsedMilliseconds+300000
do{
 if(Test-Path "$Evidence\host-update-complete"){break}
 if($clock.ElapsedMilliseconds -gt $deadline){throw 'Host completion not observed; preserve active client for diagnosis'}
 Start-Sleep -Milliseconds 500
}while($true)
$after=API private-summary|ConvertFrom-Json
$after|ConvertTo-Json|Set-Content "$Evidence\host-down-state.json"
Probe 'host-down-no-public-fallback' $false $false
Probe 'host-down-no-wan-fallback' $true $false
DNSCache 'cache-protected'
Marker 'disconnect-start'
API disconnect|Out-Null
Marker 'disconnect-end'
DNSCache 'cache-disconnected'
$idle=API private-summary|ConvertFrom-Json
if($idle.state -ne 'CONNECTION_STATE_DISCONNECTED'){throw 'Client disconnect not complete'}
API private-remove-target $target|Out-Null
$final=API private-summary|ConvertFrom-Json
if($final.saved_targets -ne $initial.saved_targets){throw 'Unrelated saved targets changed'}
API shutdown|Out-Null
(Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(60))
$delta=@(Compare-Object $baseline @(Network))
@{delta=$delta}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\network-after.json"
if($delta.Count){throw 'Client network differs from baseline'}
try{Probe 'ordinary-after' $true $true}catch{
 if($DNSForensic){
  $failure=$_
  DNSCache 'cache-after-failed-probe'
  $ErrorActionPreference='Continue'
  Marker 'direct-dns-start'
  $interface=Get-NetIPAddress -IPAddress $PhysicalSource -AddressFamily IPv4
  $servers=(Get-DnsClientServerAddress -InterfaceIndex $interface.InterfaceIndex -AddressFamily IPv4).ServerAddresses
  Resolve-DnsName speed.cloudflare.com -Type A -Server $servers[0] -DnsOnly -QuickTimeout *> "$Evidence\direct-dns.log"
  Marker 'direct-dns-end'
  try{Probe 'diagnostic-only-followup' $true $true}catch{$_|Out-String|Set-Content "$Evidence\followup-error.txt"}
  $ErrorActionPreference='Stop'
  throw $failure
 }
 throw
}
@{result='PASS';private_user_traffic_before=$true;no_public_fallback_after=$true;physical_probe_blocked_after=$true;ordinary_wan_after_disconnect=$true;saved_targets_preserved=$true;network_baseline_equal=$true;continuous_packet_capture=$false}|ConvertTo-Json|Set-Content "$Evidence\client-verdict.json"
Get-FileHash $PSCommandPath,$API,'C:\Program Files\IRVIO-W08\irviod.exe'|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
Get-Content "$Evidence\client-verdict.json"
