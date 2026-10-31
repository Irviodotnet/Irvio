# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [Parameter(Mandatory=$true)][string]$Source,
    [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
if ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne 'S-1-5-18') { throw 'Requires SYSTEM lab agent' }
if (Get-Service IRVIO -ErrorAction SilentlyContinue) { throw 'Existing IRVIO service must not be overwritten' }
if (Test-Path $Evidence) { throw 'Evidence already exists' }
$env:PATH='C:\Tools\go\bin;'+$env:PATH
$env:GOPROXY='off'
$env:GOMODCACHE='C:\Users\IrvioLab\go\pkg\mod'
$env:GOCACHE='C:\IRVIO-Lab\go-system-cache'
New-Item -ItemType Directory $Evidence | Out-Null
Set-Location $Source
& cmd.exe /d /c "go vet ./internal/localapi ./internal/uibridge ./cmd/irvio-ui-bridge > $Evidence\vet.log 2>&1"
if ($LASTEXITCODE) { throw 'Vet failed' }
& cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=10 -timeout=3m -run=TestDesktopService ./internal/localapi > $Evidence\activation.log 2>&1"
if ($LASTEXITCODE) { throw 'Activation tests failed' }
& cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=20 -timeout=2m ./internal/uibridge > $Evidence\relay.log 2>&1"
if ($LASTEXITCODE) { throw 'Relay tests failed' }
go test -c -gcflags=all=-d=checkptr=2 -o ($Evidence+'\localapi.test.exe') ./internal/localapi
if ($LASTEXITCODE) { throw 'Test build failed' }
go build '-ldflags=-H=windowsgui' -o ($Evidence+'\irvio-ui-bridge.exe') ./cmd/irvio-ui-bridge
if ($LASTEXITCODE) { throw 'Bridge build failed' }
$env:PATH='C:\Tools\flutter\bin;'+$env:PATH
$env:PUB_CACHE='C:\IRVIO-Lab\ui-pub-cache-01'
$env:FLUTTER_SUPPRESS_ANALYTICS='true'
Push-Location (Join-Path $Source 'ui')
try {
    & cmd.exe /d /c "flutter.bat pub get --offline --enforce-lockfile > $Evidence\pub.log 2>&1"
    if ($LASTEXITCODE) { throw 'Offline pub get failed' }
    & cmd.exe /d /c "flutter.bat analyze --no-pub > $Evidence\analyze.log 2>&1"
    if ($LASTEXITCODE) { throw 'Flutter analyze failed' }
    & cmd.exe /d /c "flutter.bat test --no-pub > $Evidence\flutter-test.log 2>&1"
    if ($LASTEXITCODE) { throw 'Flutter tests failed' }
    & cmd.exe /d /c "dart.bat compile exe bin\windows_local_api_smoke.dart -o $Evidence\windows-local-api-smoke.exe > $Evidence\dart-build.log 2>&1"
    if ($LASTEXITCODE) { throw 'Dart smoke compile failed' }
} finally { Pop-Location }
$accounts=@('IrvioBridgeAllow','IrvioBridgeDeny')
foreach ($name in $accounts) { if (Get-LocalUser $name -ErrorAction SilentlyContinue) { throw 'Account fixture already exists' } }
$created=@()
$serviceCreated=$false
try {
    foreach ($i in 0..1) {
        $random=New-Object byte[] 24
        $rng=[Security.Cryptography.RandomNumberGenerator]::Create()
        try { $rng.GetBytes($random) } finally { $rng.Dispose() }
        $plain='aA1!'+[Convert]::ToBase64String($random)
        $secret=ConvertTo-SecureString $plain -AsPlainText -Force
        try {
            $user=New-LocalUser -Name $accounts[$i] -Password $secret -Description 'Disposable IRVIO UI bridge probe'
            $created+=$accounts[$i]
            Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545') -Member $user
            $prefix=@('IRVIO_PIPE_ALLOWED','IRVIO_PIPE_DENIED')[$i]
            [Environment]::SetEnvironmentVariable($prefix+'_USER',$accounts[$i],'Process')
            [Environment]::SetEnvironmentVariable($prefix+'_PASSWORD',$plain,'Process')
        } finally { $secret.Dispose(); $plain=$null }
    }
    $sid=(Get-LocalUser $accounts[0]).SID.Value
    icacls.exe $Evidence /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-32-545:(OI)(CI)RX' | Out-Null
    if ($LASTEXITCODE) { throw 'Fixture ACL failed' }
    $command='"'+$Evidence+'\localapi.test.exe" -test.run=^TestWindowsDesktopBridgeServiceFixture$ -- \\.\pipe\IRVIO.Control '+$sid
    New-Service -Name IRVIO -BinaryPathName $command -StartupType Manual | Out-Null
    $serviceCreated=$true
    sc.exe sdset IRVIO ('D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;CCLCRP;;;'+$sid+')') | Out-Null
    if ($LASTEXITCODE) { throw 'Narrow service DACL failed' }
    $env:IRVIO_BRIDGE_TEST_BINARY=$Evidence+'\irvio-ui-bridge.exe'
    $env:IRVIO_BRIDGE_DART_SMOKE=$Evidence+'\windows-local-api-smoke.exe'
    & cmd.exe /d /c "$Evidence\localapi.test.exe -test.run=^TestWindowsDesktopBridgeNonAdmin$ -test.v -test.timeout=2m > $Evidence\nonadmin.log 2>&1"
    $code=$LASTEXITCODE
    Get-Content ($Evidence+'\nonadmin.log')
    if ($code) { throw 'Non-admin bridge gate failed' }
    Stop-Service IRVIO
    (Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(10))
    $foreignCommand=$command.Replace('\\.\pipe\IRVIO.Control','\\.\pipe\IRVIO.BridgeActual')
    sc.exe config IRVIO binPath= $foreignCommand | Out-Null
    if ($LASTEXITCODE) { throw 'Foreign pipe fixture configuration failed' }
    $env:IRVIO_BRIDGE_FOREIGN_PIPE='1'
    & cmd.exe /d /c "$Evidence\localapi.test.exe -test.run=^TestWindowsDesktopBridgeRejectsForeignPipe$ -test.v -test.timeout=1m > $Evidence\foreign-pipe.log 2>&1"
    $code=$LASTEXITCODE
    Get-Content ($Evidence+'\foreign-pipe.log')
    if ($code) { throw 'Foreign pipe authentication gate failed' }
} finally {
    Remove-Item Env:IRVIO_BRIDGE_TEST_BINARY -ErrorAction SilentlyContinue
    Remove-Item Env:IRVIO_BRIDGE_DART_SMOKE -ErrorAction SilentlyContinue
    Remove-Item Env:IRVIO_BRIDGE_FOREIGN_PIPE -ErrorAction SilentlyContinue
    if ($serviceCreated) {
        Stop-Service IRVIO
        (Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(10))
        sc.exe delete IRVIO | Out-Null
        if ($LASTEXITCODE) { throw 'Fixture deletion failed' }
    }
    foreach ($prefix in @('IRVIO_PIPE_ALLOWED','IRVIO_PIPE_DENIED')) {
        [Environment]::SetEnvironmentVariable($prefix+'_USER',$null,'Process')
        [Environment]::SetEnvironmentVariable($prefix+'_PASSWORD',$null,'Process')
    }
    foreach ($name in $created) { Remove-LocalUser $name }
}
if (Get-Service IRVIO -ErrorAction SilentlyContinue) { throw 'Service residue' }
if (Get-Process irvio-ui-bridge -ErrorAction SilentlyContinue) { throw 'Helper residue' }
foreach ($name in $accounts) { if (Get-LocalUser $name -ErrorAction SilentlyContinue) { throw 'Account residue' } }
Write-Output 'Bridge activation, standard-user IPC, GUI EOF and fixture cleanup PASS'
