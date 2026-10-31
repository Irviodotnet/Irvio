# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$API,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$ExpectedUpstream,
 [Parameter(Mandatory=$true)][string]$PhysicalEgress,
 [ValidateRange(1,5)][int]$ExpectedClients=1,
 [ValidateRange(30,600)][int]$DeadlineSeconds=300
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$pipe='\\.\pipe\IRVIO.Control'
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(-not(Test-Path -LiteralPath $API -PathType Leaf)){throw 'Missing Local API helper'}
if($ExpectedUpstream -eq $PhysicalEgress){throw 'Distinct upstream and physical egress oracles required'}
$service=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
if($service.State -ne 'Running' -or $service.PathName -ne '"C:\Program Files\IRVIO-W08\irviod.exe"'){throw 'Not the owned product fixture'}
New-Item -ItemType Directory $Evidence | Out-Null
$seen=$false
$released=$false
$connectedSince=$null
$clock=[Diagnostics.Stopwatch]::StartNew()
while($clock.Elapsed.TotalSeconds -lt $DeadlineSeconds) {
 $raw=& $API status $pipe
 if($LASTEXITCODE){throw 'Status query failed'}
 $raw | Add-Content "$Evidence\status.ndjson"
 $status=$raw | ConvertFrom-Json
 if($status.state -ne 'CONNECTION_STATE_CONNECTED' -or -not $status.contribution){throw 'Own CLIENT+EXIT session not active'}
 if($null -eq $connectedSince){$connectedSince=$status.connection.connected_since_unix_ms}
 if(-not $connectedSince -or $connectedSince -ne $status.connection.connected_since_unix_ms){throw 'Own connection was replaced during downstream lifecycle'}
 $raw=& $API traffic $pipe
 if($LASTEXITCODE){throw 'Traffic query failed'}
 $raw | Add-Content "$Evidence\traffic.ndjson"
 $traffic=$raw | ConvertFrom-Json
 if($traffic.clients -ge $ExpectedClients){$seen=$true}
 $egress=& curl.exe --noproxy '*' --fail --silent --show-error --max-time 12 https://api.ipify.org
 if($LASTEXITCODE -or $egress.Trim() -ne $ExpectedUpstream){throw 'Own upstream HTTPS failed or wrong egress'}
 @{elapsed_ms=$clock.ElapsedMilliseconds;clients=$traffic.clients;egress=$egress.Trim()} | ConvertTo-Json -Compress | Add-Content "$Evidence\samples.ndjson"
 if($seen -and -not $traffic.clients){$released=$true;break}
 Start-Sleep -Seconds 2
}
if(-not $seen -or -not $released){throw 'Expected downstream admission/release transition not observed'}
'PRODUCT DUAL-ROLE: expected downstream count observed, released to zero, own upstream survived'
