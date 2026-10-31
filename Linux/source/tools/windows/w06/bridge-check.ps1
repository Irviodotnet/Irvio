# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [ValidateRange(1,20)][int]$Count=3,
 [switch]$BlockControl
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$rule='IRVIO-LAB-W06-TCP-CONTROL'
$blockRule='IRVIO-LAB-W06-TCP-CONTROL-BLOCK'
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(!(Test-Path -LiteralPath $TestBinary -PathType Leaf)){throw 'Test executable missing'}
if(Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue){throw 'Existing lab firewall rule'}
if(Get-NetFirewallRule -Name $blockRule -ErrorAction SilentlyContinue){throw 'Existing lab block rule'}
if(Get-NetFirewallRule -DisplayName 'IRVIO.Control.*' -ErrorAction SilentlyContinue){throw 'Existing native control firewall rule'}
if(Get-NetRoute | Where-Object { $_.DestinationPrefix -like '10.253.246.*' -or $_.DestinationPrefix -like '10.253.247.*' }){throw 'Fixture prefixes already routed'}
$before=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid)
$null=New-Item -ItemType Directory -Path $Evidence
Get-FileHash -LiteralPath $TestBinary | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binary.json')
$previous=$env:IRVIO_WINDOWS_BRIDGE_TEST
$env:IRVIO_WINDOWS_BRIDGE_TEST='1'
$ownedBlock=$false
try {
 if($BlockControl){
  $Count=1
  New-NetFirewallRule -Name $blockRule -DisplayName $blockRule -Direction Inbound -Action Block -Protocol TCP -Program $TestBinary -LocalAddress 10.253.246.1 -LocalPort 7890 -RemoteAddress 10.253.246.0/24 -Profile Any | Out-Null
  $ownedBlock=$true
 }
 $p=Start-Process -FilePath $TestBinary -ArgumentList @('-test.v','-test.run=^TestNativeWindowsDualRoleBridge$',"-test.count=$Count",'-test.timeout=5m') -PassThru -Wait -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence 'native.log') -RedirectStandardError (Join-Path $Evidence 'native-stderr.log')
 $log=Get-Content -Raw (Join-Path $Evidence 'native.log')
 if($BlockControl){
  if($p.ExitCode -ne 1 -or $log -notmatch 'native TCP control timeout' -or $log -match '--- PASS:|panic:'){throw 'Explicit administrator block was not proven'}
  Write-Output 'Explicit administrator block respected PASS'
 }else{
  if($p.ExitCode -ne 0){throw "Native bridge tests failed: $($p.ExitCode)"}
  if(([regex]::Matches($log,'--- PASS: TestNativeWindowsDualRoleBridge')).Count -ne $Count){throw 'Missing native TCP bridge proof'}
  Write-Output "Native TCP bridge PASS count=$Count"
 }
} finally {
 if($ownedBlock){Remove-NetFirewallRule -Name $blockRule}
 if($null -eq $previous){Remove-Item Env:IRVIO_WINDOWS_BRIDGE_TEST -ErrorAction SilentlyContinue}else{$env:IRVIO_WINDOWS_BRIDGE_TEST=$previous}
 $after=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid)
 $delta=@(Compare-Object $before $after)
 $remainingRules=@(Get-NetFirewallRule -DisplayName 'IRVIO.Control.*' -ErrorAction SilentlyContinue)
 $remainingRules+=@(Get-NetFirewallRule -Name $blockRule -ErrorAction SilentlyContinue)
 $remainingRoutes=@(Get-NetRoute | Where-Object { $_.DestinationPrefix -like '10.253.246.*' -or $_.DestinationPrefix -like '10.253.247.*' })
 [pscustomobject]@{adapter_delta=$delta.Count;rule_residue=$remainingRules.Count;route_residue=$remainingRoutes.Count;secure_boot=(Confirm-SecureBootUEFI);firewall=(Get-NetFirewallProfile | Select-Object Name,Enabled);defender=(Get-MpComputerStatus | Select-Object AntivirusEnabled,RealTimeProtectionEnabled)} | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'cleanup.json')
 if($delta.Count -or $remainingRules.Count -or $remainingRoutes.Count){throw 'Native bridge cleanup residue'}
}
