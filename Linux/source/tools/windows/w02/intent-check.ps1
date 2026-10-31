# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [Parameter(Mandatory=$true)][string]$Source,
    [Parameter(Mandatory=$true)][string]$Evidence,
    [ValidateSet('tests','seed','same','resume','new')][string]$Phase='tests',
    [ValidateRange(1,100)][int]$Count=10
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne 'S-1-5-18'){throw 'Use the SYSTEM lab agent'}
$env:PATH='C:\Tools\go\bin;'+$env:PATH
$env:GOPROXY='off'
$env:GOMODCACHE='C:\Users\IrvioLab\go\pkg\mod'
$env:GOCACHE='C:\IRVIO-Lab\go-system-cache'
$env:IRVIO_INTENT_NATIVE='1'
if(-not (Test-Path (Join-Path $Source 'go.mod'))){throw 'Missing source'}
Set-Location $Source
if($Phase -eq 'tests'){
    if(Test-Path $Evidence){throw 'Evidence already exists'}
    New-Item -ItemType Directory $Evidence | Out-Null
    & cmd.exe /d /c "go vet ./internal/... > $Evidence\vet.log 2>&1"
    if($LASTEXITCODE){throw 'Native internal vet failed'}
    & cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=$Count -v -timeout=5m ./internal/productintent ./internal/identity > $Evidence\test.log 2>&1"
    $code=$LASTEXITCODE
    Get-Content "$Evidence\test.log" -Tail 35
    if($code){throw 'Native intent tests failed'}
    & cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -run=^TestConcurrentLoadOrCreateKeepsOneIdentity$ -count=100 ./internal/identity > $Evidence\identity-concurrent.log 2>&1"
    if($LASTEXITCODE){throw 'Concurrent identity publication failed'}
    go test -c -gcflags=all=-d=checkptr=2 -o "$Evidence\intent.test.exe" ./internal/productintent
    if($LASTEXITCODE){throw 'Native fixture build failed'}
    & cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=1 -timeout=5m ./internal/... > $Evidence\broad.log 2>&1"
    $code=$LASTEXITCODE
    Get-Content "$Evidence\broad.log" -Tail 25
    if($code){throw 'Broad internal gates failed'}
}else{
    $log=Join-Path $Evidence "boot-$Phase.log"
    if(Test-Path $log){throw 'Boot phase evidence exists'}
    $env:IRVIO_INTENT_BOOT_MODE=$Phase
    if($Phase -eq 'resume'){$env:IRVIO_INTENT_BOOT_MODE='same'}
    $env:IRVIO_INTENT_BOOT_PATH=Join-Path $Evidence 'boot-runtime\session-state'
    & "$Evidence\intent.test.exe" '-test.v' '-test.run=^TestWindowsNativeIntentBootFixture$' *> $log
    $code=$LASTEXITCODE
    Get-Content $log
    if($code){throw "Boot fixture phase failed: $Phase"}
}
if(Get-Process -Name 'productintent.test','intent.test' -ErrorAction SilentlyContinue){throw 'Intent test process remains'}
Write-Output "Intent phase $Phase PASS; this is not a full VPN lifecycle acceptance"
