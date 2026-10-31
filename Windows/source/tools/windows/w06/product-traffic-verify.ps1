# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
$own=Get-Content -Raw "$Evidence\own-traffic\after.json" | ConvertFrom-Json
$final=Get-Content -Raw "$Evidence\host-final-traffic.json" | ConvertFrom-Json
$stopped=Get-Content -Raw "$Evidence\disconnected-traffic.json" | ConvertFrom-Json
$samples=@(Get-Content "$Evidence\five-measured\traffic.ndjson" | ForEach-Object {$_ | ConvertFrom-Json})
if(-not $samples.Count -or ($samples | Measure-Object clients -Maximum).Maximum -ne 5){throw 'Five admitted clients not observed'}
if($final.clients -ne 0 -or $stopped.clients -ne 0 -or $stopped.active){throw 'Release/Disconnect not completed'}
foreach($value in @($own,$final,$stopped)+$samples) {
 if($value.session_id -ne $own.session_id -or -not $value.complete){throw 'Incomplete/replaced traffic session'}
 foreach($name in @('my_upload_bytes','my_download_bytes','shared_upload_bytes')) {
  if($null -eq $value.$name){throw "Missing counter: $name"}
 }
}
foreach($name in @('my_upload_bytes','my_download_bytes','shared_upload_bytes')) {
 $previous=[uint64]$own.$name
 foreach($value in $samples+@($final,$stopped)) {
  if([uint64]$value.$name -lt $previous){throw "Counter reset: $name"}
  $previous=[uint64]$value.$name
 }
}
$shared=[uint64]$final.shared_upload_bytes-[uint64]$own.shared_upload_bytes
$ownDownload=[uint64]$final.my_download_bytes-[uint64]$own.my_download_bytes
if($shared -lt 5242880 -or $shared -ge 10485760){throw 'Expected five 1 MiB downstream uploads, without counting ten 1 MiB downloads'}
if($ownDownload -ge 1048576){throw 'Unexpected own download during observer-only interval; inspect background traffic'}
@{
 samples=$samples.Count
 shared_upload_delta=$shared
 own_download_delta=$ownDownload
 disconnected_my_upload=$stopped.my_upload_bytes
 disconnected_my_download=$stopped.my_download_bytes
 disconnected_shared_upload=$stopped.shared_upload_bytes
} | ConvertTo-Json | Set-Content "$Evidence\traffic-verdict.json"
'TRAFFIC PASS: five clients released; own/shared separated; monotonic totals retained after Disconnect'
