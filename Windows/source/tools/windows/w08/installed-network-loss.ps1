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
 [Parameter(Mandatory=$true)][string]$AdapterMAC,
 [string]$ReplacementSource,
 [string]$AlternateAdapterMAC,
 [switch]$DiagnoseDNS,
 [string]$DNSProbe,
 [string]$TrafficPhaseFile,
 [string]$Country='NL',
 [ValidateRange(15,60)][int]$OutageSeconds=30,
 [switch]$DisposableVM
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if(-not $DisposableVM){throw 'Disposable VM and out-of-band management required'}
if($DNSProbe -and -not(Test-Path -LiteralPath $DNSProbe -PathType Leaf)){throw 'DNS probe executable missing'}
if($TrafficPhaseFile -and -not(Test-Path -LiteralPath $TrafficPhaseFile -PathType Leaf)){throw 'Traffic phase file missing'}
. (Join-Path $Scripts 'software-update.ps1')
$installed=Read-IRVIOInstallation
if((Get-Service IRVIO).Status -ne 'Stopped'){throw 'Stopped installed desktop required'}
$adapters=@(Get-NetAdapter|Where-Object MacAddress -eq $AdapterMAC)
if($adapters.Count -ne 1 -or $adapters[0].Status -ne 'Up'){throw 'Unique active lab adapter required'}
$adapter=$adapters[0]
$sources=@(Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4|Where-Object {$_.AddressState -eq 'Preferred' -and $_.IPAddress -notlike '169.254.*'})
if($sources.Count -ne 1){throw 'Unique physical IPv4 source required'}
$physicalSource=$sources[0].IPAddress
$originalSource=$physicalSource
$gateway=@(Get-NetRoute -InterfaceIndex $adapter.ifIndex -DestinationPrefix '0.0.0.0/0')
if($AlternateAdapterMAC){
 if($ReplacementSource){throw 'Select source replacement or alternate NIC, not both'}
 $alternates=@(Get-NetAdapter|Where-Object MacAddress -eq $AlternateAdapterMAC)
 if($alternates.Count -ne 1 -or $alternates[0].Status -ne 'Up' -or $alternates[0].ifIndex -eq $adapter.ifIndex){throw 'Distinct active alternate NIC required'}
 $alternate=$alternates[0]
 $alternateSources=@(Get-NetIPAddress -InterfaceIndex $alternate.ifIndex -AddressFamily IPv4|Where-Object {$_.AddressState -eq 'Preferred' -and $_.IPAddress -notlike '169.254.*'})
 if($alternateSources.Count -ne 1 -or $alternateSources[0].IPAddress -eq $originalSource){throw 'Unique alternate source required'}
 $alternateSource=$alternateSources[0].IPAddress
 if(-not(Get-NetRoute -InterfaceIndex $alternate.ifIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue)){throw 'Alternate default route required'}
 $selected=@(Find-NetRoute -RemoteIPAddress '1.1.1.1'|Select-Object -ExpandProperty InterfaceIndex -Unique)
 if($selected.Count -ne 1 -or $selected[0] -ne $adapter.ifIndex){throw 'Kernel must initially select primary adapter'}
}
if($ReplacementSource){
 $parsed=[Net.IPAddress]::Parse($ReplacementSource)
 if($parsed.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork -or $sources[0].PrefixLength -ne 24 -or
    ($ReplacementSource -replace '\.[^.]+$','') -ne ($originalSource -replace '\.[^.]+$','') -or
    $ReplacementSource -eq $originalSource -or $gateway.Count -ne 1 -or
    $ReplacementSource -eq $gateway[0].NextHop -or $parsed.GetAddressBytes()[3] -in @(0,255)){
  throw 'Replacement must be a distinct reserved lab address in the same /24'
 }
 if((Get-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4).Dhcp -ne 'Enabled'){throw 'Source-change fixture requires DHCP baseline'}
 if(Get-NetIPAddress -IPAddress $ReplacementSource -ErrorAction SilentlyContinue){throw 'Replacement already assigned locally'}
}
if(Test-Path $Evidence){throw 'New evidence directory required'}
New-UpdateDirectory $Evidence
$clock=[Diagnostics.Stopwatch]::StartNew()
$pipe='\\.\pipe\IRVIO.Control'
$probeAddress=@(Resolve-DnsName 'speed.cloudflare.com' -Type A -DnsOnly|Where-Object IPAddress|Select-Object -ExpandProperty IPAddress)[0]
if(-not $probeAddress){throw 'Probe IPv4 resolution failed before Connect'}
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
function Sample([string]$Stage){
 $state=API status|ConvertFrom-Json
 @{elapsed_ms=$clock.ElapsedMilliseconds;stage=$Stage;status=$state}|ConvertTo-Json -Depth 8 -Compress|Add-Content "$Evidence\states.ndjson"
 return $state
}
function TrafficPhase([string]$Phase){
 if(-not $TrafficPhaseFile){return}
 Set-Content -LiteralPath $TrafficPhaseFile -Value $Phase -Encoding ascii
 @{elapsed_ms=$clock.ElapsedMilliseconds;phase=$Phase}|ConvertTo-Json -Compress|Add-Content "$Evidence\traffic-phases.ndjson"
}
function Probe([string]$Name,[bool]$Physical,[bool]$Expected,[bool]$Pinned=$false){
 $args=@('--noproxy','*','--fail','--silent','--show-error','--connect-timeout','3','--max-time','8','--output','NUL','--write-out','%{size_download}')
 if($Physical){$args+=@('--interface',$physicalSource)}
 if($Physical -or $Pinned){$args+=@('--resolve',"speed.cloudflare.com:443:$probeAddress")}
 $args+='https://speed.cloudflare.com/__down?bytes=65536'
 $ErrorActionPreference='Continue'
 $started=$clock.ElapsedMilliseconds
 $bytes=& curl.exe @args 2> "$Evidence\$Name-error.log"
 $code=$LASTEXITCODE
 @{elapsed_ms=$started;duration_ms=$clock.ElapsedMilliseconds-$started;exit_code=$code;bytes="$bytes";physical=$Physical}|ConvertTo-Json|Set-Content "$Evidence\$Name.json"
 if($Expected){if($code -or [uint64]$bytes -ne 65536){throw "Transfer failed: $Name"}}elseif($code -eq 0 -or [uint64]$bytes -ne 0){throw "Physical-source probe leaked: $Name"}
}
function DNSForensics {
 $start=$clock.ElapsedMilliseconds
 Get-DnsClientServerAddress|Select-Object InterfaceIndex,InterfaceAlias,AddressFamily,ServerAddresses|ConvertTo-Json -Depth 5|Set-Content "$Evidence\dns-servers-at-failure.json"
 Get-NetIPInterface|Select-Object InterfaceIndex,InterfaceAlias,AddressFamily,InterfaceMetric,ConnectionState,Dhcp|ConvertTo-Json|Set-Content "$Evidence\dns-interfaces-at-failure.json"
 Find-NetRoute -RemoteIPAddress '1.1.1.1'|Select-Object InterfaceIndex,IPAddress,DestinationPrefix,NextHop,RouteMetric|ConvertTo-Json|Set-Content "$Evidence\dns-route-at-failure.json"
 try{
  @(Get-DnsClientCache -ErrorAction Stop|Where-Object Entry -eq 'speed.cloudflare.com'|Select-Object Entry,RecordName,RecordType,Status,Data)|ConvertTo-Json|Set-Content "$Evidence\dns-cache-at-failure.json"
 }catch{$_|Out-String|Set-Content "$Evidence\dns-cache-read-error.log"}
 try{Probe 'connected-after-pinned' $false $true $true}catch{$_|Out-String|Set-Content "$Evidence\pinned-probe-error.log"}
 foreach($query in @(@{name='system-a';type='A';server=''},@{name='system-aaaa';type='AAAA';server=''},@{name='direct-a';type='A';server='1.1.1.1'})){
  $at=$clock.ElapsedMilliseconds
  $records=@();$failure=$null
  try{
   $params=@{Name='speed.cloudflare.com';Type=$query.type;DnsOnly=$true;QuickTimeout=$true;ErrorAction='Stop'}
   if($query.server){$params.Server=$query.server}
   $records=@(Resolve-DnsName @params|Select-Object Name,Type,IPAddress)
  }catch{$failure=$_.Exception.Message}
  @{elapsed_ms=$at;duration_ms=$clock.ElapsedMilliseconds-$at;records=$records;error=$failure}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\dns-$($query.name).json"
 }
 try{Probe 'connected-after-dns-observation' $false $true}catch{$_|Out-String|Set-Content "$Evidence\observed-probe-error.log"}
 @{elapsed_ms=$start;duration_ms=$clock.ElapsedMilliseconds-$start;cache_flushed=$false;source_still_replaced=[bool]$ReplacementSource}|ConvertTo-Json|Set-Content "$Evidence\dns-forensics.json"
}
function UncachedDNS([string]$Name) {
 if(-not $DNSProbe){return}
 $at=$clock.ElapsedMilliseconds
 $raw=& $DNSProbe 'speed.cloudflare.com.'
 $code=$LASTEXITCODE
 @{elapsed_ms=$at;exit_code=$code;result=($raw|ConvertFrom-Json)}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\dns-uncached-$Name.json"
 if($code){throw "Uncached DNS failed: $Name"}
}
$baseline=@(Network)
ConvertTo-Json -InputObject $baseline|Set-Content "$Evidence\network-before.json"
Get-FileHash "$($installed.program)\daemon\irviod.exe",$PSCommandPath|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
if($DNSProbe){Get-FileHash $DNSProbe|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\dns-probe-input.json"}
Probe 'ordinary-before' $true $true
if($AlternateAdapterMAC){
 $physicalSource=$alternateSource
 try{Probe 'ordinary-alternate-before' $true $true}finally{$physicalSource=$originalSource}
 Get-NetIPInterface -AddressFamily IPv4|Select-Object InterfaceIndex,InterfaceAlias,InterfaceMetric,AutomaticMetric|ConvertTo-Json|Set-Content "$Evidence\interface-metrics.json"
}
Start-Service IRVIO
(Get-Service IRVIO).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
if((Sample 'idle').state -ne 'CONNECTION_STATE_DISCONNECTED'){throw 'Initial idle required'}
API import-file $BootstrapFile|Out-Null
API connect $Country|Out-Null
$deadline=$clock.ElapsedMilliseconds+120000
while((Sample 'connect').state -ne 'CONNECTION_STATE_CONNECTED'){
 if($clock.ElapsedMilliseconds -gt $deadline){throw 'Initial connection deadline'}
 Start-Sleep -Milliseconds 500
}
Probe 'connected-before' $false $true
UncachedDNS 'before'
TrafficPhase 'protected'
Probe 'physical-blocked-before' $true $false
$daemon=Get-Process -Id (Get-CimInstance Win32_Service -Filter "Name='IRVIO'").ProcessId
$disabled=$false
$sourceChanged=$false
$manualGateway=$false
try{
 $faultAt=$clock.ElapsedMilliseconds
 if($ReplacementSource){
  $sourceChanged=$true
  New-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress $ReplacementSource -PrefixLength 24 -PolicyStore ActiveStore|Out-Null
  Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress $originalSource -ErrorAction SilentlyContinue|Remove-NetIPAddress -Confirm:$false
  if(-not (Get-NetRoute -InterfaceIndex $adapter.ifIndex -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue)){
   New-NetRoute -InterfaceIndex $adapter.ifIndex -DestinationPrefix '0.0.0.0/0' -NextHop $gateway[0].NextHop -RouteMetric $gateway[0].RouteMetric -PolicyStore ActiveStore|Out-Null
   $manualGateway=$true
  }
  $physicalSource=$ReplacementSource
  $addressDeadline=$clock.ElapsedMilliseconds+15000
  while((Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress $ReplacementSource).AddressState -ne 'Preferred'){
   if($clock.ElapsedMilliseconds -gt $addressDeadline){throw 'Replacement source did not become usable'}
   Start-Sleep -Milliseconds 100
  }
  if(Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress $originalSource -ErrorAction SilentlyContinue){throw 'Old source remained assigned'}
  @{addresses=@(Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4|Select-Object IPAddress,PrefixLength,AddressState,PrefixOrigin);routes=@(Get-NetRoute -InterfaceIndex $adapter.ifIndex -DestinationPrefix '0.0.0.0/0'|Select-Object NextHop,RouteMetric);old_source=$originalSource;new_source=$ReplacementSource}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\source-replaced.json"
 }else{
  $disabled=$true
  Disable-NetAdapter -Name $adapter.Name -Confirm:$false
  $downState=(Get-NetAdapter -Name $adapter.Name).Status
  if($downState -ne 'Disabled'){throw 'Adapter disable not confirmed'}
 }
 $degraded=$false
 if($AlternateAdapterMAC){
  $physicalSource=$alternateSource
  $state=Sample 'alternate-activated'
  $degraded=$state.state -ne 'CONNECTION_STATE_CONNECTED'
 }
 while(-not $ReplacementSource -and -not $AlternateAdapterMAC -and $clock.ElapsedMilliseconds -lt $faultAt+$OutageSeconds*1000){
  $state=Sample 'adapter-down'
  if($state.state -in @('CONNECTION_STATE_RECOVERING','CONNECTION_STATE_NO_EXIT')){$degraded=$true}
  Start-Sleep -Milliseconds 500
 }
 $returnAt=$clock.ElapsedMilliseconds
 if(-not $ReplacementSource -and -not $AlternateAdapterMAC){
  Enable-NetAdapter -Name $adapter.Name -Confirm:$false
  $disabled=$false
 }else{
  $returnAt=$faultAt
 }
 Probe 'physical-blocked-return' $true $false
 $deadline=$clock.ElapsedMilliseconds+180000
 do{
  $state=Sample 'adapter-return'
  if($state.state -ne 'CONNECTION_STATE_CONNECTED'){$degraded=$true}
  if($state.state -eq 'CONNECTION_STATE_CONNECTED' -and $degraded){break}
  if($clock.ElapsedMilliseconds -gt $deadline){throw 'Automatic recovery deadline; no new Connect sent'}
  Start-Sleep -Milliseconds 500
 }while($true)
 $connectedAt=$clock.ElapsedMilliseconds
 try{
  Probe 'connected-after' $false $true
  $trafficAt=$clock.ElapsedMilliseconds
  UncachedDNS 'after'
 }catch{
  $originalFailure=$_
  if($DiagnoseDNS -and ((Test-Path "$Evidence\dns-uncached-after.json") -or (Get-Content "$Evidence\connected-after.json" -Raw|ConvertFrom-Json).exit_code -eq 6)){
   try{DNSForensics}catch{$_|Out-String|Set-Content "$Evidence\dns-forensics-error.log"}
  }
  throw $originalFailure
 }
 Probe 'physical-blocked-after' $true $false
 if($AlternateAdapterMAC){
  if((Get-NetAdapter -Name $adapter.Name).Status -ne 'Disabled'){throw 'Primary NIC returned before alternate traffic proof'}
  if((Get-NetIPAddress -InterfaceIndex $alternate.ifIndex -IPAddress $alternateSource).AddressState -ne 'Preferred'){throw 'Alternate source disappeared'}
  @{primary_ifindex=$adapter.ifIndex;alternate_ifindex=$alternate.ifIndex;primary_source=$originalSource;alternate_source=$alternateSource;primary_disabled=$true;default_routes=@(Get-NetRoute -InterfaceIndex $alternate.ifIndex -DestinationPrefix '0.0.0.0/0'|Select-Object InterfaceIndex,NextHop,RouteMetric);daemon_sockets=@(Get-NetTCPConnection -OwningProcess $daemon.Id -ErrorAction SilentlyContinue|Select-Object LocalAddress,LocalPort,State)}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\alternate-proof.json"
 }
 if($ReplacementSource){
  if((Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress $ReplacementSource).AddressState -ne 'Preferred'){throw 'Replacement source lost before traffic proof'}
  if(Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress $originalSource -ErrorAction SilentlyContinue){throw 'Old source returned before traffic proof'}
  Get-NetTCPConnection -OwningProcess $daemon.Id -ErrorAction SilentlyContinue|Select-Object LocalAddress,LocalPort,State|ConvertTo-Json|Set-Content "$Evidence\daemon-local-sockets.json"
 }
 $current=Get-Process -Id (Get-CimInstance Win32_Service -Filter "Name='IRVIO'").ProcessId
 if($current.Id -ne $daemon.Id -or $current.StartTime -ne $daemon.StartTime){throw 'Daemon restarted during adapter fault'}
 if(-not $degraded){throw 'Degraded state was not observed'}
}finally{
 if($disabled){
  Enable-NetAdapter -Name $adapter.Name -Confirm:$false
  if($AlternateAdapterMAC){
   $restoreDeadline=$clock.ElapsedMilliseconds+30000
   while(-not(Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress $originalSource -ErrorAction SilentlyContinue|Where-Object AddressState -eq 'Preferred')){
    if($clock.ElapsedMilliseconds -gt $restoreDeadline){throw 'Primary address restoration deadline'}
    Start-Sleep -Milliseconds 200
   }
   $physicalSource=$originalSource
  }
 }
 if($sourceChanged){
  Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress $ReplacementSource -ErrorAction SilentlyContinue|Remove-NetIPAddress -Confirm:$false
  if($manualGateway){Get-NetRoute -InterfaceIndex $adapter.ifIndex -DestinationPrefix '0.0.0.0/0' -NextHop $gateway[0].NextHop -ErrorAction SilentlyContinue|Remove-NetRoute -Confirm:$false}
  Set-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -Dhcp Enabled
  $renew=Start-Process ipconfig.exe -ArgumentList @('/renew',('"'+$adapter.Name+'"')) -PassThru -NoNewWindow -RedirectStandardOutput "$Evidence\dhcp-restore.log" -RedirectStandardError "$Evidence\dhcp-restore-error.log"
  if(-not $renew.WaitForExit(30000)){throw 'DHCP restoration timed out; inspect guest via agent'}
  if($renew.ExitCode){throw 'DHCP restoration failed'}
  $physicalSource=$originalSource
 }
}
TrafficPhase 'teardown'
API disconnect|Out-Null
if((Sample 'disconnected').state -ne 'CONNECTION_STATE_DISCONNECTED'){throw 'Disconnect did not return idle'}
API shutdown|Out-Null
(Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(60))
$delta=@(Compare-Object $baseline @(Network))
@{delta=$delta}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\network-after.json"
if($delta.Count){throw 'Network baseline mismatch'}
Probe 'ordinary-after' $true $true
Stop-IRVIOForMaintenance|Out-Null
$binary=Join-Path $installed.program 'daemon\irviod.exe'
try{Assert-IRVIOMaintenanceClean $binary (Get-FileHash $binary).Hash|ConvertTo-Json|Set-Content "$Evidence\native-clean.json"}finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
TrafficPhase 'after'
@{result='PASS';source_change=[bool]$ReplacementSource;interface_change=[bool]$AlternateAdapterMAC;uncached_dns=[bool]$DNSProbe;fault_ms=$faultAt;enable_requested_ms=$returnAt;return_to_connected_ms=$connectedAt-$returnAt;return_to_completed_user_probe_ms=$trafficAt-$returnAt;daemon_preserved=$true;fresh_connect_after_fault=$false;network_baseline_equal=$true;native_cleanup=$true;continuous_leak_capture=$false}|ConvertTo-Json|Set-Content "$Evidence\verdict.json"
Get-Content "$Evidence\verdict.json"
