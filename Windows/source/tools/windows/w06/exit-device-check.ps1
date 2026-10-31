# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$RuntimeTestBinary,
 [Parameter(Mandatory=$true)][string]$TunnelTestBinary,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(Test-Path $Evidence){throw 'Evidence already exists'}
foreach($binary in @($RuntimeTestBinary,$TunnelTestBinary)){
 if(!(Test-Path -LiteralPath $binary -PathType Leaf)){throw 'Missing native test executable'}
}
if(Get-NetFirewallRule -DisplayName 'IRVIO.Control.*' -ErrorAction SilentlyContinue){throw 'Existing native control rule'}
$before=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid)
function Routes {
 @(Get-NetRoute | ForEach-Object { "$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)" } | Sort-Object)
}
$routesBefore=Routes
$null=New-Item -ItemType Directory -Path $Evidence
Get-FileHash -LiteralPath $RuntimeTestBinary,$TunnelTestBinary | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binaries.json')
$previousTUN=$env:IRVIO_WINDOWS_TUN_TEST
$previousExit=$env:IRVIO_WINDOWS_EXIT_DEVICE_TEST
try {
 $env:IRVIO_WINDOWS_TUN_TEST='1'
 $env:IRVIO_WINDOWS_EXIT_DEVICE_TEST='1'
 $p=Start-Process $TunnelTestBinary -ArgumentList @('-test.v','-test.run=^TestWindowsNativeDualRoleSourceSelection$','-test.count=3','-test.timeout=2m') -Wait -PassThru -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence 'source.log') -RedirectStandardError (Join-Path $Evidence 'source-stderr.log')
 if($p.ExitCode -ne 0){throw 'Native source selection failed'}
 $p=Start-Process $RuntimeTestBinary -ArgumentList @('-test.v','-test.run=^TestNativeWindowsExitDeviceOwnership$','-test.count=3','-test.timeout=3m') -Wait -PassThru -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence 'device.log') -RedirectStandardError (Join-Path $Evidence 'device-stderr.log')
 if($p.ExitCode -ne 0){throw 'Native runtime Exit device failed'}
 foreach($check in @(@('source.log','TestWindowsNativeDualRoleSourceSelection'),@('device.log','TestNativeWindowsExitDeviceOwnership'))){
  $log=Get-Content -Raw (Join-Path $Evidence $check[0])
  if(([regex]::Matches($log,('--- PASS: '+$check[1]))).Count -ne 3){throw 'Native checks were skipped/incomplete'}
 }
 Write-Output 'Native Exit device and dual-role source selection PASS; full Exit runtime not yet exercised'
} finally {
 if($null -eq $previousTUN){Remove-Item Env:IRVIO_WINDOWS_TUN_TEST -ErrorAction SilentlyContinue}else{$env:IRVIO_WINDOWS_TUN_TEST=$previousTUN}
 if($null -eq $previousExit){Remove-Item Env:IRVIO_WINDOWS_EXIT_DEVICE_TEST -ErrorAction SilentlyContinue}else{$env:IRVIO_WINDOWS_EXIT_DEVICE_TEST=$previousExit}
 $after=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid)
 $delta=@(Compare-Object $before $after)
 $routeDelta=@(Compare-Object $routesBefore (Routes))
 $remainingRules=@(Get-NetFirewallRule -DisplayName 'IRVIO.Control.*' -ErrorAction SilentlyContinue)
 [pscustomobject]@{adapter_delta=$delta;route_delta=$routeDelta;control_rules=$remainingRules.Count;secure_boot=(Confirm-SecureBootUEFI);firewall=(Get-NetFirewallProfile | Select-Object Name,Enabled);defender=(Get-MpComputerStatus | Select-Object AntivirusEnabled,RealTimeProtectionEnabled)} | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'cleanup.json')
 if($delta.Count -or $routeDelta.Count -or $remainingRules.Count){throw 'Native Exit device cleanup residue'}
}
