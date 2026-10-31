# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Binary,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use an absolute lab path without shell metacharacters'}
if(Test-Path $Evidence){throw 'Evidence exists'}
New-Item -ItemType Directory $Evidence | Out-Null
$exe=Join-Path $Evidence 'firewall.test.exe'
Copy-Item $Binary $exe
Get-FileHash $exe | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binary.json')
$rules=@(Get-NetFirewallRule | Where-Object {$_.Group -eq 'IRVIO.Inbound.v1'} | Select-Object -ExpandProperty Name | Sort-Object)
$env:IRVIO_WINDOWS_INBOUND_TEST='1'
try {
 & cmd.exe /d /c "$exe -test.run=^TestNativePhysicalInbound$ -test.v -test.count=3 -test.timeout=90s > $Evidence\native.log 2>&1"
 $result=$LASTEXITCODE
 Get-Content "$Evidence\native.log" -Tail 40
 if($result){throw 'Native inbound gate failed'}
 if(([regex]::Matches((Get-Content -Raw "$Evidence\native.log"),'--- PASS: TestNativePhysicalInbound')).Count -ne 3){throw 'Native gate skipped'}
} finally {
 Remove-Item Env:IRVIO_WINDOWS_INBOUND_TEST
 $after=@(Get-NetFirewallRule | Where-Object {$_.Group -eq 'IRVIO.Inbound.v1'} | Select-Object -ExpandProperty Name | Sort-Object)
 if(($rules.Count -ne $after.Count) -or (($rules.Count -gt 0) -and (Compare-Object $rules $after))){throw 'Firewall residue'}
 Write-Output 'Firewall rule baseline restored'
}
