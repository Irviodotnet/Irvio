# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [Parameter(Mandatory = $true)][string]$TestBinary,
    [Parameter(Mandatory = $true)][string]$SourceIPv4,
    [Parameter(Mandatory = $true)][string]$OutputDirectory,
    [ValidateRange(1, 20)][int]$Count = 3
)

$ErrorActionPreference = 'Stop'
if (!(Test-Path -LiteralPath $TestBinary -PathType Leaf)) { throw 'Missing Exit NAT test executable' }
if (Test-Path -LiteralPath $OutputDirectory) { throw 'Output directory must be new' }
$null = New-Item -ItemType Directory -Path $OutputDirectory
$env:IRVIO_WINDOWS_NAT_PORT_SOURCE = $SourceIPv4
$env:GODEBUG = 'checkptr=2'
Get-FileHash -Algorithm SHA256 -LiteralPath $TestBinary | ConvertTo-Json | Set-Content (Join-Path $OutputDirectory 'binary.json')
Get-NetIPInterface -AddressFamily IPv4 | Select-Object InterfaceAlias,InterfaceIndex,Forwarding,WeakHostSend,ConnectionState | ConvertTo-Json | Set-Content (Join-Path $OutputDirectory 'interfaces-before.json')
$log = Join-Path $OutputDirectory 'tests.log'
$process = Start-Process -FilePath $TestBinary -ArgumentList @('-test.v', '-test.shuffle=on', "-test.count=$Count", '-test.timeout=15m') -Wait -PassThru -NoNewWindow -RedirectStandardOutput $log -RedirectStandardError (Join-Path $OutputDirectory 'stderr.log')
Get-NetIPInterface -AddressFamily IPv4 | Select-Object InterfaceAlias,InterfaceIndex,Forwarding,WeakHostSend,ConnectionState | ConvertTo-Json | Set-Content (Join-Path $OutputDirectory 'interfaces-after.json')
Get-Content -LiteralPath $log -Tail 25
if ($process.ExitCode -ne 0) { throw "Exit NAT tests failed: $($process.ExitCode); see $OutputDirectory" }
if (!(Select-String -LiteralPath $log -Pattern '^--- PASS: TestWindowsPortReservation100Cycles' -Quiet)) { throw 'Native port test did not pass' }
Write-Output 'Exit NAT component checks: PASS; no runtime/driver integration claimed'
