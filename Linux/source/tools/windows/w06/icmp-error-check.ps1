# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [Parameter(Mandatory=$true)][string]$ExitTestBinary,
    [Parameter(Mandatory=$true)][string]$DriverTestBinary,
    [Parameter(Mandatory=$true)][string]$SourceIPv4,
    [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference = 'Stop'
if (Test-Path -LiteralPath $Evidence) { throw 'Evidence directory must be new' }
if (Get-Service IRVIO,IRVIO-Recovery -ErrorAction SilentlyContinue) { throw 'Remove product lab fixture before component tests' }
if (Get-CimInstance Win32_SystemDriver | Where-Object { $_.Name -like '*WinDivert*' -or $_.PathName -like '*WinDivert*' }) { throw 'Existing driver; refusing ownership' }
$driver = Join-Path $env:ProgramFiles 'IRVIO Exit Driver\WinDivert64.sys'
if ((Get-FileHash -LiteralPath $driver).Hash -ne '8da085332782708d8767bcace5327a6ec7283c17cfb85e40b03cd2323a90ddc2') { throw 'Unexpected driver payload' }
if ((Get-AuthenticodeSignature -LiteralPath $driver).Status -ne 'Valid') { throw 'Unsigned driver' }
$null = New-Item -ItemType Directory -Path $Evidence
Get-FileHash -LiteralPath $ExitTestBinary,$DriverTestBinary,$driver | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binaries.json')
function State {
    [ordered]@{
        routes = @(Get-NetRoute -AddressFamily IPv4 | Sort-Object InterfaceIndex,DestinationPrefix,NextHop | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric)
        dns = @(Get-DnsClientServerAddress | Sort-Object InterfaceIndex,AddressFamily | Select-Object InterfaceIndex,AddressFamily,ServerAddresses)
    } | ConvertTo-Json -Depth 6 -Compress
}
$before = State
$before | Set-Content (Join-Path $Evidence 'host-before.json')
$created = $false
try {
    & sc.exe create WinDivert type= kernel start= demand binPath= $driver
    if ($LASTEXITCODE) { throw 'Driver registration failed' }
    $created = $true
    & sc.exe start WinDivert
    if ($LASTEXITCODE) { throw 'Driver start failed' }
    $env:IRVIO_DIVERT_TEST_SERVICE = 'running'
    $env:IRVIO_DIVERT_IO_SOURCE = $SourceIPv4
    $env:IRVIO_WINDOWS_NAT_PORT_SOURCE = $SourceIPv4
    $env:IRVIO_WINDOWS_NAT_DNS_ORACLE = '1.1.1.1'
    $env:GODEBUG = 'checkptr=2'
    foreach ($spec in @(
        @{ name='icmp-driver'; binary=$DriverTestBinary; run='^TestNative(ICMPError|Overlapped)' },
        @{ name='exitnat'; binary=$ExitTestBinary; run='.' }
    )) {
        $log = Join-Path $Evidence ($spec.name+'.log')
        $proc = Start-Process -FilePath $spec.binary -ArgumentList @('-test.v',('-test.run='+$spec.run),'-test.shuffle=on','-test.count=3','-test.timeout=8m') -NoNewWindow -Wait -PassThru -RedirectStandardOutput $log -RedirectStandardError (Join-Path $Evidence ($spec.name+'.stderr.log'))
        Get-Content $log -Tail 12
        if ($proc.ExitCode) { throw ($spec.name+' failed: '+$proc.ExitCode) }
    }
    foreach ($name in @('TestNativeICMPErrorFilter','TestNativeICMPErrorCapture')) {
        if (!(Select-String -LiteralPath (Join-Path $Evidence 'icmp-driver.log') -Pattern ('^--- PASS: '+$name+' ') -Quiet)) { throw ('Missing native gate '+$name) }
    }
    foreach ($name in @('TestICMPErrorReverseTranslation','TestICMPErrorOwnershipFences','TestNativeBackendFiveLeaseDNS','TestWindowsPortReservation100Cycles')) {
        if (!(Select-String -LiteralPath (Join-Path $Evidence 'exitnat.log') -Pattern ('^--- PASS: '+$name+' ') -Quiet)) { throw ('Missing NAT gate '+$name) }
    }
} finally {
    if ($created) {
        $service = Get-CimInstance Win32_SystemDriver -Filter "Name='WinDivert'"
        if (!$service -or $service.PathName.Replace('\??\','').Trim('"') -ne $driver) { throw 'Unexpected service; refusing cleanup' }
        & sc.exe stop WinDivert
        if ($LASTEXITCODE -and $LASTEXITCODE -ne 1062) { throw 'Driver stop failed' }
        & sc.exe delete WinDivert
        if ($LASTEXITCODE) { throw 'Driver delete failed' }
    }
    $after = State
    $after | Set-Content (Join-Path $Evidence 'host-after.json')
    if ($before -cne $after) { throw 'Host route/DNS residue; see raw snapshots' }
    if (Get-CimInstance Win32_SystemDriver | Where-Object Name -like '*WinDivert*') { throw 'Driver residue' }
}
'ICMP TCP/UDP errors: PASS; echo and full product PMTU acceptance remain open'
