# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Evidence, [string]$Binary='C:\IRVIO-Lab\W05\protected.test.exe',
    [ValidateSet('TestWindowsNativeProtectedController','TestWindowsNativeOwnedUnderlay')][string]$TestName='TestWindowsNativeProtectedController')
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Binary -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$' -or $Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use absolute lab paths without shell metacharacters'}
if(Test-Path $Evidence){throw 'Evidence directory already exists'}
$service=Get-CimInstance Win32_Service -Filter "Name='IRVIO-W05-Owner'"
if(!$service -or $service.PathName -ne 'C:\IRVIO-Lab\W05\filterprobe.exe service' -or $service.StartMode -ne 'Auto' -or $service.State -ne 'Running'){throw 'Expected disposable service fixture missing'}
$name='IrvioWfpProbe'
if(Get-LocalUser -Name $name -ErrorAction SilentlyContinue){throw 'Probe user already exists'}
New-Item -ItemType Directory $Evidence | Out-Null
$bytes=[byte[]]::new(24)
$rng=[Security.Cryptography.RandomNumberGenerator]::Create();$rng.GetBytes($bytes);$rng.Dispose()
$plain='aA1!'+[Convert]::ToBase64String($bytes)
$secret=ConvertTo-SecureString $plain -AsPlainText -Force
$created=$false
$exe=$Binary
try{
    $user=New-LocalUser -Name $name -Password $secret -Description 'Temporary IRVIO W05 traffic authorization test' -AccountNeverExpires
    $created=$true
    Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545') -Member $user
    $env:IRVIO_WFP_PROBE_USER=$name;$env:IRVIO_WFP_PROBE_PASSWORD=$plain
    $env:IRVIO_WINDOWS_PROTECTION_TEST='1'
    & cmd.exe /d /c "$exe -test.run=^$TestName$ -test.v -test.count=3 -test.timeout=5m > $Evidence\native-repeat.log 2>&1"
    if($LASTEXITCODE){Get-Content ($Evidence+'\native-repeat.log') -Tail 35;throw 'Native protection gate failed'}
    Get-Content ($Evidence+'\native-repeat.log') -Tail 35
}finally{
    Remove-Item Env:IRVIO_WFP_PROBE_USER,Env:IRVIO_WFP_PROBE_PASSWORD,Env:IRVIO_WINDOWS_PROTECTION_TEST -ErrorAction SilentlyContinue
    if($created){Remove-LocalUser -Name $name}
    $secret.Dispose();$plain=$null
    $env:IRVIO_WINDOWS_PROTECTION_CLEANUP='1'
    & $exe '-test.run=^TestWindowsNativeProtectionCleanup$' '-test.v'
    $code=$LASTEXITCODE
    Remove-Item Env:IRVIO_WINDOWS_PROTECTION_CLEANUP
    if($code){throw 'Exact-owner WFP cleanup failed'}
}
