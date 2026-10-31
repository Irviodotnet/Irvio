# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [ValidateRange(1,10)][int]$Count=3
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(!(Test-Path -LiteralPath $TestBinary -PathType Leaf)){throw 'Test executable missing'}
if(Get-NetFirewallRule -DisplayName 'IRVIO.Control.*' -ErrorAction SilentlyContinue){throw 'Existing owned control firewall rule'}
if(Get-NetRoute | Where-Object {$_.DestinationPrefix -like '10.253.244.*'}){throw 'Fixture prefix already routed'}
$before=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid)
$null=New-Item -ItemType Directory -Path $Evidence
Get-FileHash -LiteralPath $TestBinary | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binary.json')
$previous=$env:IRVIO_WINDOWS_FIREWALL_TEST
$env:IRVIO_WINDOWS_FIREWALL_TEST='1'
try {
 $p=Start-Process -FilePath $TestBinary -ArgumentList @('-test.v','-test.run=TestCOMRule|TestOwnedControl|TestControlJournal|TestNativeControlCrashRecovery',"-test.count=$Count",'-test.timeout=5m') -PassThru -Wait -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence 'native.log') -RedirectStandardError (Join-Path $Evidence 'native-stderr.log')
 if($p.ExitCode -ne 0){throw "Control recovery failed: $($p.ExitCode)"}
 $log=Get-Content -Raw (Join-Path $Evidence 'native.log')
 if(([regex]::Matches($log,'real owner process kill -> exact journal recovery -> zero rule residue PASS')).Count -ne 2*$Count){throw 'Missing native crash recovery proof'}
 Write-Output "Native control recovery PASS: $($Count*2) process kills"
} finally {
 if($null -eq $previous){Remove-Item Env:IRVIO_WINDOWS_FIREWALL_TEST -ErrorAction SilentlyContinue}else{$env:IRVIO_WINDOWS_FIREWALL_TEST=$previous}
 $delta=@(Compare-Object $before @(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid))
 $rules=@(Get-NetFirewallRule -DisplayName 'IRVIO.Control.*' -ErrorAction SilentlyContinue)
 $routes=@(Get-NetRoute | Where-Object {$_.DestinationPrefix -like '10.253.244.*'})
 [pscustomobject]@{adapter_delta=$delta.Count;rules=$rules.Count;routes=$routes.Count;secure_boot=(Confirm-SecureBootUEFI);firewall=(Get-NetFirewallProfile | Select-Object Name,Enabled);defender=(Get-MpComputerStatus | Select-Object AntivirusEnabled,RealTimeProtectionEnabled)} | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'cleanup.json')
 if($delta.Count -or $rules.Count -or $routes.Count){throw 'Control recovery residue; preserve journal and diagnose without broad cleanup'}
}
