# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [Parameter(Mandatory=$true)][string]$SourceAddress,
 [Parameter(Mandatory=$true)][string]$OracleAddress,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(-not [Security.Principal.WindowsIdentity]::GetCurrent().IsSystem){throw 'Requires SYSTEM in isolated Windows lab'}
if(Test-Path $Evidence){throw 'Existing evidence; refusing overwrite'}
if(Get-Service IRVIO,IRVIO-Recovery -ErrorAction SilentlyContinue){throw 'Existing product installation'}
if(Get-CimInstance Win32_SystemDriver | Where-Object {$_.Name -like '*WinDivert*' -or $_.PathName -like '*WinDivert*'}){throw 'Existing driver'}
$driver=Join-Path $env:ProgramFiles 'IRVIO Exit Driver\WinDivert64.sys'
if((Get-FileHash $driver).Hash -ne '8da085332782708d8767bcace5327a6ec7283c17cfb85e40b03cd2323a90ddc2'){throw 'Wrong pinned driver'}
if((Get-AuthenticodeSignature $driver).Status -ne 'Valid'){throw 'Invalid driver signature'}
& ping.exe -S $SourceAddress -n 2 -w 1000 $OracleAddress
if($LASTEXITCODE){throw 'Oracle underlay preflight failed'}
New-Item -ItemType Directory $Evidence | Out-Null
function State {
 [ordered]@{
  routes=@(Get-NetRoute | Sort-Object InterfaceIndex,AddressFamily,DestinationPrefix,NextHop | Select-Object InterfaceIndex,AddressFamily,DestinationPrefix,NextHop,RouteMetric)
  dns=@(Get-DnsClientServerAddress | Sort-Object InterfaceIndex,AddressFamily | Select-Object InterfaceIndex,AddressFamily,ServerAddresses)
  adapters=@(Get-NetAdapter -IncludeHidden | Sort-Object InterfaceGuid | Select-Object InterfaceGuid,Name)
 } | ConvertTo-Json -Depth 6 -Compress
}
$before=State
$before | Set-Content (Join-Path $Evidence 'host-before.json')
Get-FileHash $TestBinary,$driver | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binaries.json')
$created=$false
try {
 & sc.exe create WinDivert type= kernel start= demand binPath= $driver
 if($LASTEXITCODE){throw 'Driver creation failed'}
 $created=$true
 & sc.exe start WinDivert
 if($LASTEXITCODE){throw 'Driver start failed'}
 $env:IRVIO_DIVERT_TEST_SERVICE='running'
 $env:IRVIO_WINDOWS_NAT_PORT_SOURCE=$SourceAddress
 $env:IRVIO_WINDOWS_NAT_ECHO_ORACLE=$OracleAddress
 $proc=Start-Process $TestBinary -ArgumentList @('-test.v','-test.run=^TestNative(BackendFiveLeaseEcho|EncryptedLeaseEcho)$','-test.count=3','-test.shuffle=on','-test.timeout=3m') -Wait -PassThru -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence 'test.log') -RedirectStandardError (Join-Path $Evidence 'stderr.log')
 if($proc.ExitCode){throw "Native backend echo failed: $($proc.ExitCode)"}
 foreach($name in @('TestNativeBackendFiveLeaseEcho','TestNativeEncryptedLeaseEcho')) {
  if((Select-String -LiteralPath (Join-Path $Evidence 'test.log') -Pattern ('^--- PASS: '+$name+' ')).Count -ne 3){throw "Missing native repetitions: $name"}
 }
 $resource=Start-Process $TestBinary -ArgumentList @('-test.v','-test.run=^TestNativeEncryptedEchoResourceLifetime$','-test.timeout=5m') -Wait -PassThru -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence 'resources.log') -RedirectStandardError (Join-Path $Evidence 'resources-stderr.log')
 if($resource.ExitCode -or !(Select-String -LiteralPath (Join-Path $Evidence 'resources.log') -Pattern '^--- PASS: TestNativeEncryptedEchoResourceLifetime ' -Quiet)){throw 'Native echo resource lifetime failed'}
} finally {
 Remove-Item Env:\IRVIO_DIVERT_TEST_SERVICE,Env:\IRVIO_WINDOWS_NAT_PORT_SOURCE,Env:\IRVIO_WINDOWS_NAT_ECHO_ORACLE -ErrorAction SilentlyContinue
 if($created){
  $s=Get-CimInstance Win32_SystemDriver -Filter "Name='WinDivert'"
  if($s.PathName.Replace('\??\','').Trim('"') -ne $driver){throw 'Unexpected driver target'}
  & sc.exe stop WinDivert
  if($LASTEXITCODE -and $LASTEXITCODE -ne 1062){throw 'Driver stop failed'}
  & sc.exe delete WinDivert
  if($LASTEXITCODE){throw 'Driver deletion failed'}
 }
 $after=State
 $after | Set-Content (Join-Path $Evidence 'host-after.json')
 if($before -cne $after){throw 'Host networking residue'}
 if(Get-Service WinDivert -ErrorAction SilentlyContinue){throw 'Driver service residue'}
}
Get-Content (Join-Path $Evidence 'test.log')
'NATIVE ECHO BACKEND + AWG/LEASE FIXTURE PASS; full product acceptance remains separate'
