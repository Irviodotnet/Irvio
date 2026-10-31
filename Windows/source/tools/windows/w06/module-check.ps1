# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [Parameter(Mandatory=$true)][string]$VendorDirectory,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [switch]$ReuseLabPayload,
 [ValidatePattern('^[A-Za-z0-9_.^$|/()\-]+$')][string]$TestPattern='.',
 [ValidateRange(1,100)][int]$RunningCount=3,
 [ValidatePattern('^[1-9][0-9]*[sm]$')][string]$TestTimeout='3m'
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$root=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Exit Driver'
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(Get-CimInstance Win32_SystemDriver | Where-Object Name -like '*WinDivert*'){throw 'Existing driver service; refusing ownership'}
if((Test-Path $root) -and !$ReuseLabPayload){throw 'Package directory already exists'}
$hashes=@{'WinDivert.dll'='c1e060ee19444a259b2162f8af0f3fe8c4428a1c6f694dce20de194ac8d7d9a2';'WinDivert64.sys'='8da085332782708d8767bcace5327a6ec7283c17cfb85e40b03cd2323a90ddc2'}
foreach($name in $hashes.Keys){if((Get-FileHash (Join-Path $VendorDirectory $name)).Hash -ne $hashes[$name]){throw 'Vendor payload hash mismatch'}}
$null=New-Item -ItemType Directory -Path $Evidence
if(!(Test-Path $root)){
 $null=New-Item -ItemType Directory -Path $root
 $acl=New-Object System.Security.AccessControl.DirectorySecurity
 $acl.SetSecurityDescriptorSddlForm('O:SYG:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)')
 Set-Acl -LiteralPath $root -AclObject $acl
 foreach($name in $hashes.Keys){
  Copy-Item -LiteralPath (Join-Path $VendorDirectory $name) -Destination (Join-Path $root $name)
  $acl=New-Object System.Security.AccessControl.FileSecurity
  $acl.SetSecurityDescriptorSddlForm('O:SYG:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)')
  Set-Acl -LiteralPath (Join-Path $root $name) -AclObject $acl
 }
}
foreach($name in $hashes.Keys){if((Get-FileHash (Join-Path $root $name)).Hash -ne $hashes[$name]){throw 'Installed payload hash mismatch'}}
Get-FileHash -LiteralPath $TestBinary | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binary.json')
function Run-Tests([string]$phase,[int]$count){
 $env:IRVIO_DIVERT_TEST_SERVICE=$phase
 $p=Start-Process -FilePath $TestBinary -ArgumentList @('-test.v','-test.shuffle=on',"-test.count=$count","-test.timeout=$TestTimeout","-test.run=$TestPattern") -PassThru -Wait -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence ($phase+'.log')) -RedirectStandardError (Join-Path $Evidence ($phase+'-stderr.log'))
 if($p.ExitCode -ne 0){throw "Driver module $phase tests failed: $($p.ExitCode)"}
}
$owned=$false
try{
 Run-Tests 'absent' 1
 if(Get-Service WinDivert -ErrorAction SilentlyContinue){throw 'Runtime installed a driver'}
 $sys=Join-Path $root 'WinDivert64.sys'
 & sc.exe create WinDivert type= kernel start= demand binPath= $sys
 if($LASTEXITCODE -ne 0){throw 'Driver registration failed'}
 $owned=$true
 & sc.exe start WinDivert
 if($LASTEXITCODE -ne 0){throw 'Driver start failed'}
 Run-Tests 'running' $RunningCount
 Write-Output 'Pinned module native tests PASS'
}finally{
 if($owned){
  $service=Get-CimInstance Win32_SystemDriver -Filter "Name='WinDivert'"
  if($service.PathName.Replace('\??\','').Trim('"') -ne (Join-Path $root 'WinDivert64.sys')){throw 'Refusing cleanup of unexpected service'}
  & sc.exe stop WinDivert
  if($LASTEXITCODE -notin @(0,1062)){throw 'Driver stop failed'}
  & sc.exe delete WinDivert
  if($LASTEXITCODE -ne 0){throw 'Driver deletion failed'}
 }
 $remaining=@(Get-CimInstance Win32_SystemDriver | Where-Object Name -like '*WinDivert*')
 [pscustomobject]@{remaining_drivers=$remaining.Count;secure_boot=(Confirm-SecureBootUEFI);firewall=(Get-NetFirewallProfile | Select-Object Name,Enabled);defender=(Get-MpComputerStatus | Select-Object AntivirusEnabled,RealTimeProtectionEnabled)} | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'cleanup.json')
 if($remaining.Count){throw 'Driver residue'}
}
