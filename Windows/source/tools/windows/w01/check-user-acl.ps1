# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$name='IrvioAclProbe'
$fixture='C:\IRVIO-Lab\W01-service-acl\state'
if (Get-LocalUser -Name $name -ErrorAction SilentlyContinue) { throw 'Probe user already exists' }
if ([IO.File]::ReadAllText($fixture) -ne 'non-secret ACL fixture') { throw 'Fixture missing or unexpected' }
if ((Get-Acl $fixture).Owner -ne 'NT AUTHORITY\SYSTEM') { throw 'Fixture must be SYSTEM-owned' }
$random=New-Object byte[] 24
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($random)
$plain='aA1!'+[Convert]::ToBase64String($random)
$secret=ConvertTo-SecureString $plain -AsPlainText -Force
$created=$false
try {
    $user=New-LocalUser -Name $name -Password $secret -Description 'Temporary IRVIO W01 ACL test' -AccountNeverExpires
    $created=$true
    Add-LocalGroupMember -Group (Get-LocalGroup -SID 'S-1-5-32-545') -Member $user
    $env:IRVIO_ACL_PROBE_USER=$name
    $env:IRVIO_ACL_PROBE_PASSWORD=$plain
    $env:IRVIO_ACL_PROBE_PATH=$fixture
    & cmd.exe /d /c 'C:\IRVIO-Lab\W01\winfile.test.exe -test.run=TestUnprivilegedServiceACL -test.v > C:\IRVIO-Lab\W00\w01-unprivileged-acl.log 2>&1'
    $code=$LASTEXITCODE
    Get-Content C:\IRVIO-Lab\W00\w01-unprivileged-acl.log
    if ($code) { throw 'Non-admin token ACL test failed' }
} finally {
    Remove-Item Env:IRVIO_ACL_PROBE_USER,Env:IRVIO_ACL_PROBE_PASSWORD,Env:IRVIO_ACL_PROBE_PATH -ErrorAction SilentlyContinue
    if ($created) { Remove-LocalUser -Name $name }
    $secret.Dispose()
}
