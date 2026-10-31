# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [Parameter(Mandatory=$true)][string]$TestBinary,
    [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference = 'Stop'
$user = 'IrvioEchoProbe'
$service = 'IRVIO-W06-EchoOwner'
if ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne 'S-1-5-18') { throw 'Requires SYSTEM in disposable QEMU VM' }
if (Get-Service IRVIO,IRVIO-Recovery,$service -ErrorAction SilentlyContinue) { throw 'Existing service owner' }
if (Get-LocalUser -Name $user -ErrorAction SilentlyContinue) { throw 'Existing test identity' }
if (Test-Path -LiteralPath $Evidence) { throw 'Evidence directory must be new' }
$null = New-Item -ItemType Directory -Path $Evidence
$binary = Join-Path $Evidence 'echo.test.exe'
Copy-Item -LiteralPath $TestBinary -Destination $binary
Get-FileHash -LiteralPath $binary | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binary.json')
function State {
    [ordered]@{
        routes=@(Get-NetRoute -AddressFamily IPv4 | Sort-Object InterfaceIndex,DestinationPrefix,NextHop | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric)
        dns=@(Get-DnsClientServerAddress | Sort-Object InterfaceIndex,AddressFamily | Select-Object InterfaceIndex,AddressFamily,ServerAddresses)
        adapters=@(Get-NetAdapter -IncludeHidden | Sort-Object InterfaceGuid | Select-Object InterfaceGuid,Name)
    } | ConvertTo-Json -Depth 6 -Compress
}
$before = State
$before | Set-Content (Join-Path $Evidence 'before.json')
$password = 'Lab!' + [Guid]::NewGuid().ToString('N') + 'a9'
$createdUser = $false
$createdService = $false
try {
    $null = New-LocalUser -Name $user -Password (ConvertTo-SecureString $password -AsPlainText -Force) -Description 'Disposable IRVIO echo token probe'
    $createdUser = $true
    Add-LocalGroupMember -SID 'S-1-5-32-545' -Member $user
    & sc.exe create $service start= auto binPath= 'C:\Windows\System32\cmd.exe /c exit 0'
    if ($LASTEXITCODE) { throw 'Service creation failed' }
    $createdService = $true
    $env:IRVIO_ECHO_IDENTITY_LAB='1'
    $env:IRVIO_ECHO_PROBE_USER=$user
    $env:IRVIO_ECHO_PROBE_PASSWORD=$password
    $proc = Start-Process $binary -ArgumentList @('-test.v','-test.run=^TestNativeEchoUserIdentityCannotIsolate$','-test.count=3','-test.shuffle=on','-test.timeout=60s') -Wait -PassThru -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence 'test.log') -RedirectStandardError (Join-Path $Evidence 'stderr.log')
} finally {
    Remove-Item Env:\IRVIO_ECHO_PROBE_PASSWORD,Env:\IRVIO_ECHO_PROBE_USER,Env:\IRVIO_ECHO_IDENTITY_LAB -ErrorAction SilentlyContinue
    $password = $null
    if ($createdUser) { Remove-LocalUser -Name $user }
    if ($createdService) {
        & sc.exe delete $service
        if ($LASTEXITCODE) { throw 'Service deletion failed' }
    }
    $after = State
    $after | Set-Content (Join-Path $Evidence 'after.json')
    if ($before -cne $after) { throw 'Host residue after identity experiment' }
    if (Get-LocalUser -Name $user -ErrorAction SilentlyContinue) { throw 'Temporary user residue' }
    if (Get-Service $service -ErrorAction SilentlyContinue) { throw 'Temporary service residue' }
}
Get-Content (Join-Path $Evidence 'test.log')
Get-Content (Join-Path $Evidence 'stderr.log')
if ($proc.ExitCode) { throw ('Identity experiment failed: '+$proc.ExitCode) }
if ((Select-String -LiteralPath (Join-Path $Evidence 'test.log') -Pattern '^--- PASS: TestNativeEchoUserIdentityCannotIsolate ').Count -ne 3) { throw 'Missing native repetitions' }
'Experiment PASS; native ICMP API rejected as ProtectedMode Exit backend; host baseline restored'
