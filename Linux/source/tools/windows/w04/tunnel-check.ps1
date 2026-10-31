# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [string]$Source='C:\src\irvio',
    [string]$Evidence='C:\IRVIO-Lab\evidence\w04-tun',
    [string]$Wintun='C:\IRVIO-Lab\W00\wintun.dll',
    [string]$Label='native',
    [int]$Count=3
)
$ErrorActionPreference='Stop'
$env:PATH='C:\Tools\go\bin;C:\Program Files\Git\cmd;'+$env:PATH
$env:GOPROXY='off'
New-Item -ItemType Directory -Force $Evidence | Out-Null
if(Test-Path "$Evidence\$Label-tests.log"){throw 'Evidence label already exists; choose a new -Label'}
Set-Location $Source

function Baseline {
    [ordered]@{
        defaults=@(Get-NetRoute | Where-Object DestinationPrefix -In @('0.0.0.0/0','::/0') | Sort-Object InterfaceIndex,DestinationPrefix | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric)
        dns=@(Get-DnsClientServerAddress | Sort-Object InterfaceIndex,AddressFamily | Select-Object InterfaceIndex,AddressFamily,ServerAddresses)
    } | ConvertTo-Json -Depth 8 -Compress
}

$before=Baseline
$before | Set-Content "$Evidence\$Label-baseline.json"
& cmd.exe /d /c "go vet ./internal/tunnel > $Evidence\$Label-vet.log 2>&1"
if($LASTEXITCODE -ne 0){throw 'tunnel vet failed'}
& cmd.exe /d /c "go test -shuffle=on -count=10 ./internal/tunnel > $Evidence\$Label-shuffle.log 2>&1"
if($LASTEXITCODE -ne 0){throw 'tunnel unit regression failed'}
& cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -c -o $Evidence\tunnel.test.exe ./internal/tunnel > $Evidence\$Label-build.log 2>&1"
if($LASTEXITCODE -ne 0){throw 'tunnel native build failed'}
Copy-Item $Wintun "$Evidence\wintun.dll" -Force
$env:IRVIO_WINDOWS_TUN_TEST='1'
try {
    & cmd.exe /d /c "$Evidence\tunnel.test.exe -test.v -test.shuffle=on -test.count=$Count -test.timeout=5m > $Evidence\$Label-tests.log 2>&1"
    $result=$LASTEXITCODE
    Get-Content "$Evidence\$Label-tests.log"
} finally {
    Remove-Item Env:IRVIO_WINDOWS_TUN_TEST -ErrorAction SilentlyContinue
    $after=Baseline
    $after | Set-Content "$Evidence\$Label-after.json"
    if($before -ne $after){throw 'default route/DNS baseline changed'}
    if(Get-NetAdapter -IncludeHidden | Where-Object Name -Like 'IRVIO-T*'){throw 'native TUN adapter residue'}
}
if($result -ne 0){throw 'native tunnel tests failed'}
Write-Output 'WINDOWS TUN: PASS; default routes/DNS restored; no owned adapters remain'
