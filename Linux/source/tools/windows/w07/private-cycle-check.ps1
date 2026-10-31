# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$API,
 [string]$Target,
 [ValidatePattern('^[A-Z]{2}$')][string]$GlobalCountry,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$Baseline,
 [Parameter(Mandatory=$true)][string]$PhysicalSource,
 [ValidateRange(1,100)][int]$Cycles=100,
 [ValidateRange(0,30)][int]$Warmup=10,
 [string]$ExpectedServicePath='"C:\Program Files\IRVIO-W08\irviod.exe"',
 [string]$HandleSampler
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$pipe='\\.\pipe\IRVIO.Control'
if(([bool]$Target) -eq ([bool]$GlobalCountry)){throw 'Choose one exact Private target or one Global country'}
$mode=if($GlobalCountry){'GLOBAL'}else{'PRIVATE'}
if(Test-Path $Evidence){throw 'Evidence exists'}
$svc=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
if($svc.PathName -ne $ExpectedServicePath){throw 'Unexpected disposable installation service path'}
if($svc.State -eq 'Stopped'){
 Start-Service IRVIO
 (Get-Service IRVIO).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
 $svc=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
}
if($svc.State -ne 'Running'){throw 'Service not ready'}
$owner=$svc.ProcessId
New-Item -ItemType Directory $Evidence | Out-Null
$clock=[Diagnostics.Stopwatch]::StartNew()
function Read-API([string]$Command) {
 $deadline=$clock.ElapsedMilliseconds+30000
 do {
  $ErrorActionPreference='Continue'
  $raw=& $API $Command $pipe 2> "$Evidence\api-error.txt"
  $code=$LASTEXITCODE
  $ErrorActionPreference='Stop'
  if(-not $code){return ($raw | ConvertFrom-Json)}
  $message=Get-Content -Raw "$Evidence\api-error.txt"
  if($message -notmatch 'busy with another lifecycle operation'){throw "API failed: $Command ($message)"}
  Emit 'api-busy' 0 @{command=$Command}
  Start-Sleep -Milliseconds 500
 } while($clock.ElapsedMilliseconds -lt $deadline)
 throw "API remained busy: $Command"
}
function Emit([string]$Event,[int]$Iteration,$Value) {
 @{elapsed_ms=$clock.ElapsedMilliseconds;iteration=$Iteration;event=$Event;value=$Value} | ConvertTo-Json -Depth 8 -Compress | Add-Content "$Evidence\events.ndjson"
}
function Idle-Network([int]$Iteration) {
 $old=Get-Content -Raw "$Baseline\routes-before.json" | ConvertFrom-Json
 $before=@($old | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$([uint32]$_.Protocol)"} | Sort-Object)
 $after=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$([uint32]$_.Protocol)"} | Sort-Object)
 $routeDelta=@(Compare-Object $before $after)
 $old=Get-Content -Raw "$Baseline\dns-before.json" | ConvertFrom-Json
 $before=@($old | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
 $after=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
 $dnsDelta=@(Compare-Object $before $after)
 $before=@((Get-Content -Raw "$Baseline\adapters-before.json" | ConvertFrom-Json).InterfaceGuid | Sort-Object)
 $after=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
 $adapterDelta=@(Compare-Object $before $after)
 if($routeDelta.Count -or $dnsDelta.Count -or $adapterDelta.Count){
  Emit 'network-residue' $Iteration @{routes=$routeDelta;dns=$dnsDelta;adapters=$adapterDelta}
  throw 'Idle network differs from baseline'
 }
}
$initial=Read-API 'private-summary'
if($initial.state -ne 'CONNECTION_STATE_DISCONNECTED' -or $initial.hosting_wanted -or ($Target -and $initial.saved_targets -lt 1)){throw 'Idle and applicable saved configuration required'}
if($GlobalCountry){
 $bootstrap=Read-API 'bootstrap'
 if($bootstrap.configured -lt 1){throw 'Existing bootstrap required; harness supplies no hidden introducer'}
}
$rows=@()
try {
 for($i=1;$i -le ($Warmup+$Cycles);$i++) {
  $start=$clock.ElapsedMilliseconds
  Emit 'connect-request' $i @{mode=$mode;hosting=([bool]$Target);country=$GlobalCountry;private_target=([bool]$Target)}
  $ErrorActionPreference='Continue'
  if($GlobalCountry){
   & $API connect $pipe $GlobalCountry > "$Evidence\connect-$i.txt" 2> "$Evidence\connect-$i-error.txt"
  } else {
   & $API private-connect-host $pipe $Target > "$Evidence\connect-$i.txt" 2> "$Evidence\connect-$i-error.txt"
  }
  $connectRejected=$LASTEXITCODE -ne 0
  $ErrorActionPreference='Stop'
  if($connectRejected){
   Emit 'connect-rejected' $i $null
   $recoveryDeadline=$clock.ElapsedMilliseconds+120000
   do {
    $s=Read-API 'private-summary'
    Emit 'automatic-recovery-status' $i $s
    if($s.state -eq 'CONNECTION_STATE_CONNECTED'){break}
    Start-Sleep -Milliseconds 500
   } while($clock.ElapsedMilliseconds -lt $recoveryDeadline)
   throw "Connect rejected: $i; automatic recovery ended in $($s.state)"
  }
  $deadline=$clock.ElapsedMilliseconds+90000
  do {
   $s=Read-API 'private-summary'
   Emit 'status' $i $s
   if($s.state -eq 'CONNECTION_STATE_CONNECTED'){break}
   if($clock.ElapsedMilliseconds -gt $deadline){throw "Connect timeout: $i"}
   Start-Sleep -Milliseconds 500
  } while($true)
  if($GlobalCountry){
   if($s.private_target_active -or $s.hosting_wanted -or -not $s.contribution){throw 'Global CLIENT+EXIT intent missing or private intent leaked'}
   $status=Read-API 'status'
   Emit 'global-status' $i $status
   if($status.country -ne $GlobalCountry -or $status.connection.exit_country_code -ne $GlobalCountry){throw 'Wrong Global country selected'}
  } elseif(-not $s.private_target_active -or -not $s.hosting_wanted -or $s.contribution){throw 'Combined private intent missing or public contribution leaked'}
  $connectMs=$clock.ElapsedMilliseconds-$start
  if($connectMs -gt 30000){
   & $API logs $pipe > "$Evidence\slow-connect-$i.json"
   if($LASTEXITCODE){throw 'Slow connection evidence unavailable'}
  }
  $traffic=Read-API 'traffic'
  Emit 'traffic-before' $i $traffic
  $bytes=& curl.exe --noproxy '*' --fail --silent --show-error --max-time 30 --output NUL --write-out '%{size_download}' 'https://speed.cloudflare.com/__down?bytes=65536'
  if($LASTEXITCODE -or [uint64]$bytes -ne 65536){throw "$mode traffic failed: $i"}
  $leak=Start-Process curl.exe -ArgumentList @('--interface',$PhysicalSource,'--noproxy','*','--fail','--silent','--show-error','--connect-timeout','1','--max-time','2','https://1.1.1.1/cdn-cgi/trace') -Wait -PassThru -NoNewWindow -RedirectStandardOutput "$Evidence\physical-$i.txt" -RedirectStandardError "$Evidence\physical-$i-error.txt"
  if($leak.ExitCode -eq 0 -or (Get-Item "$Evidence\physical-$i.txt").Length -ne 0){throw 'Physical-source HTTPS leak'}
  $traffic=Read-API 'traffic'
  Emit 'traffic-after' $i $traffic
  $start=$clock.ElapsedMilliseconds
  & $API disconnect $pipe
  if($LASTEXITCODE){throw "Disconnect failed: $i"}
  $s=Read-API 'private-summary'
  if($s.state -ne 'CONNECTION_STATE_DISCONNECTED' -or $s.hosting_wanted -or $s.private_target_active -or $s.contribution -or $s.saved_targets -ne $initial.saved_targets){throw 'Disconnect did not clear participation or changed saved grants'}
  $disconnectMs=$clock.ElapsedMilliseconds-$start
  $finalTraffic=Read-API 'traffic'
  Emit 'final-traffic' $i $finalTraffic
  if($finalTraffic.active -or $finalTraffic.clients -or -not $finalTraffic.complete -or $finalTraffic.my_download_bytes -lt 65536 -or $finalTraffic.shared_upload_bytes){throw 'Final session traffic missing or misattributed'}
  Idle-Network $i
  $svc=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
  if($svc.State -ne 'Running' -or $svc.ProcessId -ne $owner){throw 'Daemon restarted during lifecycle gate'}
  $proc=Get-Process -Id $owner
  $allTCP=@(Get-NetTCPConnection -ErrorAction SilentlyContinue | Where-Object {$_.OwningProcess -eq $owner})
  $tcp=@($allTCP | Where-Object {$_.State -notin @('TimeWait','FinWait2')})
  $closingTCP=@($allTCP | Where-Object {$_.State -eq 'FinWait2'})
  if($closingTCP.Count){
   $closingTCP | Select-Object LocalAddress,LocalPort,RemoteAddress,RemotePort,@{n='State';e={$_.State.ToString()}},OwningProcess,CreationTime | ConvertTo-Json | Set-Content "$Evidence\kernel-fin-wait2-$i.json"
  }
  $udp=@(Get-NetUDPEndpoint -ErrorAction SilentlyContinue | Where-Object {$_.OwningProcess -eq $owner})
  $row=[pscustomobject]@{iteration=$i;warmup=($i -le $Warmup);connect_ms=$connectMs;disconnect_ms=$disconnectMs;handles=$proc.HandleCount;threads=$proc.Threads.Count;rss_bytes=$proc.WorkingSet64;private_bytes=$proc.PrivateMemorySize64;tcp=$tcp.Count;tcp_fin_wait2=$closingTCP.Count;udp=$udp.Count;download_bytes=[uint64]$bytes;physical_https_blocked=$true}
  $rows+=$row
  $row | Export-Csv "$Evidence\cycles.csv" -Append -NoTypeInformation
  if($HandleSampler){
   $types=& $HandleSampler -ProcessID $owner
   @{iteration=$i;types=$types} | ConvertTo-Json -Depth 8 -Compress | Add-Content "$Evidence\handle-types.ndjson"
  }
  if($tcp.Count -or $udp.Count){
   $tcp | Select-Object LocalAddress,LocalPort,RemoteAddress,RemotePort,State,OwningProcess,CreationTime | ConvertTo-Json | Set-Content "$Evidence\idle-tcp-$i.json"
   $udp | Select-Object LocalAddress,LocalPort,OwningProcess,CreationTime | ConvertTo-Json | Set-Content "$Evidence\idle-udp-$i.json"
   for($sample=1;$sample -le 5;$sample++){
    Start-Sleep -Seconds 1
    Get-NetTCPConnection -ErrorAction SilentlyContinue | Where-Object {$_.OwningProcess -eq $owner} | Select-Object LocalAddress,LocalPort,RemoteAddress,RemotePort,State,OwningProcess | ConvertTo-Json | Set-Content "$Evidence\idle-tcp-$i-after-$sample.json"
   }
   throw 'Idle daemon retains network sockets'
  }
  Emit 'idle-clean' $i $row
  "$mode CYCLE $i/$($Warmup+$Cycles) PASS"
 }
 @{result='PASS';mode=$mode;country=$GlobalCountry;warmup=$Warmup;measured_cycles=$Cycles;network_cleanup_each_cycle=$true;physical_https_blocked_each_cycle=$true;resource_assessment='REQUIRES DISTRIBUTION/TREND REVIEW';elapsed_ms=$clock.ElapsedMilliseconds} | ConvertTo-Json | Set-Content "$Evidence\verdict.json"
} catch {
 $_ | Out-String | Set-Content "$Evidence\failure.txt"
 & $API logs $pipe > "$Evidence\failure-logs.json"
 throw
} finally {
 & $API disconnect $pipe > "$Evidence\final-disconnect.txt"
 if($LASTEXITCODE){throw 'Final disconnect failed; retain fixture'}
}
