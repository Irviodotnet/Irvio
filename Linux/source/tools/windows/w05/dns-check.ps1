# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [Parameter(Mandatory=$true)][string]$Evidence,
    [ValidateSet('Backend','Resolver','QuickResolver','RoutedResolver','RecoveryResolver','RecoveryPolicy')][string]$Mode='Backend'
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use an absolute evidence path without spaces or shell metacharacters'}
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(!(Test-Path C:\IRVIO-Lab\W05\wintun.dll)){throw 'Signed Wintun DLL missing from lab binary directory'}
$packet=$Mode -ne 'Backend'
if($packet -and (Get-Service IRVIO-W05-Owner -ErrorAction SilentlyContinue)){throw 'Fixture service already exists'}
foreach($name in @('IRVIO_DNS_NATIVE_TEST','IRVIO_DNS_PACKET_TEST','IRVIO_DNS_PIN_TEST_THREAD','IRVIO_DNS_QUICK_TEARDOWN','IRVIO_DNS_PROTECTION_CLEANUP','IRVIO_DNS_ROUTES_TEST','IRVIO_DNS_RECOVERY_TEST')){
    if(Test-Path ('Env:'+ $name)){throw ('Unexpected test environment: '+$name)}
}
New-Item -ItemType Directory $Evidence | Out-Null
& cmd.exe /d /c "go vet ./internal/windns ./internal/wfp ./internal/wintest > $Evidence\vet.log 2>&1"
if($LASTEXITCODE){throw 'Vet failed; inspect vet.log'}
& cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=10 -v ./internal/windns ./internal/wfp > $Evidence\unit.log 2>&1"
if($LASTEXITCODE){throw 'Unit tests failed; inspect unit.log'}
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W05\dns.test.exe ./internal/windns
if($LASTEXITCODE){throw 'Native test build failed'}
$created=$false
try{
    $run='^TestNativeDNS'
    if($packet){
        go build -o C:\IRVIO-Lab\W05\filterprobe.exe ./tools/windows/w05/filterprobe
        if($LASTEXITCODE){throw 'Fixture build failed'}
        $baseline='baseline-'+[guid]::NewGuid().ToString('N')+'.irvio.invalid'
        $baseline | Out-File -Encoding utf8 ($Evidence+'\positive-control.txt')
        try{Resolve-DnsName $baseline -DnsOnly -NoHostsFile -QuickTimeout -ErrorAction Stop | Out-Null}catch{}
        New-Service -Name IRVIO-W05-Owner -BinaryPathName 'C:\IRVIO-Lab\W05\filterprobe.exe service' -StartupType Automatic | Out-Null
        $created=$true
        Start-Service IRVIO-W05-Owner
        $env:IRVIO_DNS_PACKET_TEST='1'
        if($Mode -eq 'QuickResolver'){$env:IRVIO_DNS_QUICK_TEARDOWN='1'}
        if($Mode -eq 'RoutedResolver'){$env:IRVIO_DNS_ROUTES_TEST='1';$env:IRVIO_DNS_QUICK_TEARDOWN='1'}
        if($Mode -eq 'RecoveryResolver'){$env:IRVIO_DNS_ROUTES_TEST='1';$env:IRVIO_DNS_RECOVERY_TEST='1'}
        if($Mode -eq 'RecoveryPolicy'){$env:IRVIO_DNS_ROUTES_TEST='1';$env:IRVIO_DNS_RECOVERY_TEST='policy'}
        $run='^TestNativeDNSOrdinaryResolver$'
    }
    $env:IRVIO_DNS_NATIVE_TEST='1'
    & cmd.exe /d /c "C:\IRVIO-Lab\W05\dns.test.exe -test.run=$run -test.count=3 -test.v -test.timeout=5m > $Evidence\native.log 2>&1"
    $result=$LASTEXITCODE
}finally{
    Remove-Item Env:IRVIO_DNS_NATIVE_TEST,Env:IRVIO_DNS_PACKET_TEST,Env:IRVIO_DNS_QUICK_TEARDOWN,Env:IRVIO_DNS_ROUTES_TEST,Env:IRVIO_DNS_RECOVERY_TEST -ErrorAction SilentlyContinue
    if($created){
        $env:IRVIO_DNS_PROTECTION_CLEANUP='1'
        & cmd.exe /d /c "C:\IRVIO-Lab\W05\dns.test.exe -test.run=^TestNativeDNSProtectionCleanup$ -test.v > $Evidence\cleanup.log 2>&1"
        $cleanup=$LASTEXITCODE
        Remove-Item Env:IRVIO_DNS_PROTECTION_CLEANUP
        if($cleanup){throw 'Exact-owner policy cleanup failed; retain fixture service and use Guest Agent'}
        Stop-Service IRVIO-W05-Owner
        sc.exe delete IRVIO-W05-Owner
        if($LASTEXITCODE){throw 'Fixture service deletion failed'}
    }
}
Get-Content ($Evidence+'\native.log') -Tail 45
if($result){throw 'Native DNS gate failed; evidence retained'}
Write-Output ('DNS '+$Mode+': PASS; packet modes additionally require independent capture analysis')
