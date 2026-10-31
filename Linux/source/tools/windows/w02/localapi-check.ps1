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
if ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne 'S-1-5-18') { throw 'Run from the SYSTEM lab agent' }
$ProgressPreference='SilentlyContinue'
$env:PATH='C:\Tools\go\bin;'+$env:PATH
$env:GOPROXY='off'
$env:GOMODCACHE='C:\Users\IrvioLab\go\pkg\mod'
$env:GOCACHE='C:\IRVIO-Lab\go-system-cache'
$e=$Evidence
if(Test-Path $e){throw 'Evidence directory already exists'}
if(-not (Test-Path (Join-Path $Source 'go.mod'))){throw 'Source go.mod missing'}
New-Item -ItemType Directory $e | Out-Null
Set-Location $source
& cmd.exe /d /c "go vet ./internal/localapi > $e\vet.log 2>&1"
if($LASTEXITCODE){Get-Content ($e+'\vet.log');throw 'Vet failed'}
& cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=$Count -v -timeout=10m ./internal/localapi > $e\test.log 2>&1"
$code=$LASTEXITCODE
Get-Content ($e+'\test.log') -Tail 40
if($code){throw 'Native Local API failed'}
go test -c -gcflags=all=-d=checkptr=2 -o ($e+'\localapi.test.exe') ./internal/localapi
if($LASTEXITCODE){throw 'ACL test build failed'}
$accounts=@('IrvioPipeAllow','IrvioPipeDeny')
foreach($name in $accounts){if(Get-LocalUser -Name $name -ErrorAction SilentlyContinue){throw 'Test account already exists'}}
$created=@()
$serviceName='IRVIO-API-Fixture-'+$PID
$pipePath='\\.\pipe\IRVIO.ServiceFixture-'+$PID
$serviceCreated=$false
if(Get-Service -Name $serviceName -ErrorAction SilentlyContinue){throw 'SCM fixture already exists'}
try {
    foreach($i in 0..1){
        $random=New-Object byte[] 24
        [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($random)
        $plain='aA1!'+[Convert]::ToBase64String($random)
        $secret=ConvertTo-SecureString $plain -AsPlainText -Force
        try {
            $user=New-LocalUser -Name $accounts[$i] -Password $secret -Description 'Disposable IRVIO Named Pipe ACL probe'
            $created+=$accounts[$i]
            Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545') -Member $user
            $prefix=@('IRVIO_PIPE_ALLOWED','IRVIO_PIPE_DENIED')[$i]
            [Environment]::SetEnvironmentVariable($prefix+'_USER',$accounts[$i],'Process')
            [Environment]::SetEnvironmentVariable($prefix+'_PASSWORD',$plain,'Process')
        } finally {$secret.Dispose()}
    }
    $sid=(Get-LocalUser -Name $accounts[0]).SID.Value
    icacls.exe $e /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-32-545:(OI)(CI)RX' | Out-Null
    if($LASTEXITCODE){throw 'Fixture binary ACL failed'}
    $command='"'+$e+'\localapi.test.exe" -test.run=^TestWindowsPipeServiceFixture$ -- '+$serviceName+' '+$pipePath+' '+$sid
    New-Service -Name $serviceName -BinaryPathName $command -StartupType Manual | Out-Null
    $serviceCreated=$true
    Start-Service -Name $serviceName
    (Get-Service -Name $serviceName).WaitForStatus('Running',[TimeSpan]::FromSeconds(10))
    $env:IRVIO_PIPE_SERVICE_NAME=$serviceName
    $env:IRVIO_PIPE_SERVICE_PATH=$pipePath
    & cmd.exe /d /c "$e\localapi.test.exe -test.run=TestWindowsNamedPipe -test.count=3 -test.shuffle=on -test.v -test.timeout=5m > $e\acl.log 2>&1"
    $code=$LASTEXITCODE
    Get-Content ($e+'\acl.log') -Tail 60
    if($code){throw 'Named Pipe non-admin ACL gate failed'}
} finally {
    Remove-Item Env:IRVIO_PIPE_SERVICE_NAME,Env:IRVIO_PIPE_SERVICE_PATH -ErrorAction SilentlyContinue
    if($serviceCreated){
        Stop-Service -Name $serviceName
        (Get-Service -Name $serviceName).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(10))
        sc.exe delete $serviceName | Out-Null
        if($LASTEXITCODE){throw 'Fixture service deletion failed'}
    }
    foreach($prefix in @('IRVIO_PIPE_ALLOWED','IRVIO_PIPE_DENIED')){
        [Environment]::SetEnvironmentVariable($prefix+'_USER',$null,'Process')
        [Environment]::SetEnvironmentVariable($prefix+'_PASSWORD',$null,'Process')
    }
    foreach($name in $created){Remove-LocalUser -Name $name}
}

if(Get-Service -Name $serviceName -ErrorAction SilentlyContinue){throw 'Fixture service remains'}
foreach($name in $accounts){if(Get-LocalUser -Name $name -ErrorAction SilentlyContinue){throw 'Fixture account remains'}}
if(Get-Process -Name 'localapi.test' -ErrorAction SilentlyContinue){throw 'Fixture test process remains'}
Write-Output 'Local API native gates and disposable SCM/account cleanup PASS'
