# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([string]$Binary='C:\IRVIO-Lab\W05\wfp.test.exe')
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$name='IrvioWfpProbe'
if(Get-LocalUser -Name $name -ErrorAction SilentlyContinue){throw 'Probe user already exists'}
$bytes=[byte[]]::new(24)
$rng=[Security.Cryptography.RandomNumberGenerator]::Create();$rng.GetBytes($bytes);$rng.Dispose()
$plain='aA1!'+[Convert]::ToBase64String($bytes)
$secret=ConvertTo-SecureString $plain -AsPlainText -Force
$created=$false
try{
    $user=New-LocalUser -Name $name -Password $secret -Description 'Temporary IRVIO W05 ACL test' -AccountNeverExpires
    $created=$true
    Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545') -Member $user
    $env:IRVIO_WFP_NATIVE_TEST='1';$env:IRVIO_WFP_PROBE_USER=$name;$env:IRVIO_WFP_PROBE_PASSWORD=$plain
    & $Binary '-test.run=^TestNativeUnprivilegedCannotChangePolicy$' '-test.v'
    if($LASTEXITCODE){throw 'WFP non-admin ACL check failed'}
}finally{
    Remove-Item Env:IRVIO_WFP_NATIVE_TEST,Env:IRVIO_WFP_PROBE_USER,Env:IRVIO_WFP_PROBE_PASSWORD -ErrorAction SilentlyContinue
    if($created){Remove-LocalUser -Name $name}
    $secret.Dispose();$plain=$null
}
