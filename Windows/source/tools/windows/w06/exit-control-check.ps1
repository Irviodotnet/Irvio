# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Binary,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [ValidateRange(1,10)][int]$Count=3,
 [switch]$WANOracle
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Binary -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$' -or $Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use absolute lab paths without shell metacharacters'}
if(Test-Path $Evidence){throw 'Evidence directory already exists'}
$service=Get-CimInstance Win32_Service -Filter "Name='IRVIO-W05-Owner'"
if(!$service -or $service.PathName -ne 'C:\IRVIO-Lab\W05\filterprobe.exe service' -or $service.StartMode -ne 'Auto' -or $service.State -ne 'Running'){throw 'Expected disposable service fixture missing'}
if(!(Test-Path $Binary)){throw 'Test binary missing'}
if(Get-NetFirewallRule -Name 'IRVIO.Control.*' -ErrorAction SilentlyContinue){throw 'Preexisting control firewall rules'}
$beforeAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
$beforeRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
New-Item -ItemType Directory $Evidence | Out-Null
Get-FileHash $Binary | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binary.json')
try {
 $env:IRVIO_WINDOWS_EXIT_CONTROL_TEST='1'
 if($WANOracle){$env:IRVIO_WINDOWS_EXIT_CONTROL_WAN_TEST='1'}
 & cmd.exe /d /c "`"$Binary`" -test.run=^TestWindowsNativeExitControlReplies$ -test.v -test.count=$Count -test.timeout=3m > `"$Evidence\native.log`" 2>&1"
 $code=$LASTEXITCODE
 Get-Content (Join-Path $Evidence 'native.log') -Tail 35
 if($code){throw 'Native Exit control gate failed'}
 $log=Get-Content -Raw (Join-Path $Evidence 'native.log')
 if(([regex]::Matches($log,'--- PASS: TestWindowsNativeExitControlReplies')).Count -ne $Count){throw 'Native gate skipped/failed'}
} finally {
 Remove-Item Env:IRVIO_WINDOWS_EXIT_CONTROL_TEST -ErrorAction SilentlyContinue
 Remove-Item Env:IRVIO_WINDOWS_EXIT_CONTROL_WAN_TEST -ErrorAction SilentlyContinue
 $env:IRVIO_WINDOWS_PROTECTION_CLEANUP='1'
 & cmd.exe /d /c "`"$Binary`" -test.run=^TestWindowsNativeProtectionCleanup$ -test.v > `"$Evidence\cleanup.log`" 2>&1"
 $cleanupCode=$LASTEXITCODE
 Remove-Item Env:IRVIO_WINDOWS_PROTECTION_CLEANUP
 $afterAdapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
 $afterRoutes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
 $rules=@(Get-NetFirewallRule -Name 'IRVIO.Control.*' -ErrorAction SilentlyContinue)
 $state=[pscustomobject]@{cleanup_code=$cleanupCode;adapter_delta=@(Compare-Object $beforeAdapters $afterAdapters);route_delta=@(Compare-Object $beforeRoutes $afterRoutes);control_rules=$rules.Count;secure_boot=(Confirm-SecureBootUEFI);defender=(Get-MpComputerStatus | Select-Object AntivirusEnabled,RealTimeProtectionEnabled);firewall=@(Get-NetFirewallProfile | Select-Object Name,Enabled)}
 $state | ConvertTo-Json -Depth 6 | Set-Content (Join-Path $Evidence 'host-cleanup.json')
 if($cleanupCode -or $state.adapter_delta.Count -or $state.route_delta.Count -or $rules.Count){throw 'Exact-owner cleanup failed or host residue remains'}
}
Write-Output 'Exit control reply gate PASS; integrated protected CLIENT+EXIT remains separate'
