# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [Parameter(Mandatory=$true)][string]$Source,
    [Parameter(Mandatory=$true)][string]$Evidence,
    [ValidateRange(1,100)][int]$Count = 10
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne 'S-1-5-18'){throw 'Use the SYSTEM lab agent'}
$env:PATH='C:\Tools\go\bin;'+$env:PATH
$env:GOPROXY='off'
$env:GOMODCACHE='C:\Users\IrvioLab\go\pkg\mod'
$env:GOCACHE='C:\IRVIO-Lab\go-system-cache'
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(-not (Test-Path (Join-Path $Source 'go.mod'))){throw 'Missing source'}
New-Item -ItemType Directory $Evidence | Out-Null
Set-Location $Source
$e=$Evidence
& cmd.exe /d /c "go vet ./internal/... > $e\vet.log 2>&1"
if($LASTEXITCODE){throw 'Native internal vet failed'}
go test -c -gcflags=all=-d=checkptr=2 -o "$e\winfile.test.exe" ./internal/winfile
if($LASTEXITCODE){throw 'ACL probe build failed'}
$name='IrvioAWGProbe'
if(Get-LocalUser -Name $name -ErrorAction SilentlyContinue){throw 'Probe account exists'}
$created=$false
try {
    $random=New-Object byte[] 24
    $rng=[Security.Cryptography.RandomNumberGenerator]::Create()
    try {$rng.GetBytes($random)} finally {$rng.Dispose()}
    $plain='aA1!'+[Convert]::ToBase64String($random)
    $secret=ConvertTo-SecureString $plain -AsPlainText -Force
    try {
        $user=New-LocalUser -Name $name -Password $secret -Description 'Disposable AWG private-file ACL probe'
        $created=$true
        Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545') -Member $user
    } finally {$secret.Dispose()}
    $env:IRVIO_ACL_PROBE_USER=$name
    $env:IRVIO_ACL_PROBE_PASSWORD=$plain
    $env:IRVIO_AWG_ACL_PROBE_EXE="$e\winfile.test.exe"
    & cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=$Count -v -timeout=5m ./internal/awgidentity ./internal/productprofile ./internal/stunprobe ./internal/localapi > $e\focused.log 2>&1"
    $code=$LASTEXITCODE
    Get-Content "$e\focused.log" -Tail 25
    if($code){throw 'AWG native focused gates failed'}
} finally {
    Remove-Item Env:IRVIO_ACL_PROBE_USER,Env:IRVIO_ACL_PROBE_PASSWORD,Env:IRVIO_AWG_ACL_PROBE_EXE -ErrorAction SilentlyContinue
    $plain=$null
    if($created){Remove-LocalUser -Name $name}
}
if(Get-LocalUser -Name $name -ErrorAction SilentlyContinue){throw 'Probe account remains'}
if(Get-Process -Name 'awgidentity.test','winfile.test' -ErrorAction SilentlyContinue){throw 'Probe process remains'}
& cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=1 -timeout=5m ./internal/... > $e\broad.log 2>&1"
$code=$LASTEXITCODE
Get-Content "$e\broad.log" -Tail 25
if($code){throw 'Broad native internal tests failed'}
Write-Output 'AWG storage, ACL, crash-lock, profile cleanup and broad native gates PASS'
