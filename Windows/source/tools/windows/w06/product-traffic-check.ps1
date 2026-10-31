# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$API,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$pipe='\\.\pipe\IRVIO.Control'
if(Test-Path $Evidence){throw 'Evidence already exists'}
New-Item -ItemType Directory $Evidence | Out-Null
function Read-Traffic([string]$Name) {
 $raw=& $API traffic $pipe
 if($LASTEXITCODE){throw 'Traffic query failed'}
 $raw | Set-Content "$Evidence\$Name.json"
 $value=$raw | ConvertFrom-Json
 foreach($field in @('clients','my_upload_bytes','my_download_bytes','shared_upload_bytes')) {
  if($null -eq $value.$field){throw "Counter unavailable: $field"}
 }
 if(-not $value.active -or -not $value.complete -or $value.clients){throw 'Expected complete active session without downstream clients'}
 return $value
}
$before=Read-Traffic 'before'
& curl.exe --noproxy '*' --fail --silent --show-error --max-time 90 --output NUL --write-out '%{size_download}' 'https://speed.cloudflare.com/__down?bytes=2097152' | Set-Content "$Evidence\download-bytes.txt"
if($LASTEXITCODE -or [uint64](Get-Content -Raw "$Evidence\download-bytes.txt") -ne 2097152){throw 'Own download failed'}
$payload="$Evidence\upload-payload.bin"
[IO.File]::WriteAllBytes($payload,(New-Object byte[] 1048576))
& curl.exe --noproxy '*' --fail --silent --show-error --max-time 90 --output NUL --write-out '%{size_upload}' --data-binary "@$payload" 'https://speed.cloudflare.com/__up' | Set-Content "$Evidence\upload-bytes.txt"
if($LASTEXITCODE -or [uint64](Get-Content -Raw "$Evidence\upload-bytes.txt") -ne 1048576){throw 'Own upload failed'}
Start-Sleep -Seconds 3
$after=Read-Traffic 'after'
if($after.session_id -ne $before.session_id){throw 'Traffic session changed'}
$delta=@{
 my_upload_bytes=([uint64]$after.my_upload_bytes-[uint64]$before.my_upload_bytes)
 my_download_bytes=([uint64]$after.my_download_bytes-[uint64]$before.my_download_bytes)
 shared_upload_bytes=([uint64]$after.shared_upload_bytes-[uint64]$before.shared_upload_bytes)
}
$delta | ConvertTo-Json | Set-Content "$Evidence\delta.json"
if($delta.my_upload_bytes -lt 1048576 -or $delta.my_download_bytes -lt 2097152 -or $delta.shared_upload_bytes -ne 0){throw 'Own payload missing or counted as downstream traffic'}
'OWN TRAFFIC: upload + download counted; SHARED unchanged'
