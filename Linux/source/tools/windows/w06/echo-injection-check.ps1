# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$DriverTestBinary,
 [Parameter(Mandatory=$true)][string]$EchoTestBinary,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$user='IrvioEchoInject'
$owner='IRVIO-W06-EchoOwner'
if(-not [Security.Principal.WindowsIdentity]::GetCurrent().IsSystem){throw 'Requires SYSTEM in disposable QEMU VM'}
if((Test-Path $Evidence) -or (Get-LocalUser $user -ErrorAction SilentlyContinue)){throw 'Existing evidence/user; refusing overwrite'}
if(Get-Service IRVIO,IRVIO-Recovery,$owner -ErrorAction SilentlyContinue){throw 'Existing product/service fixture'}
if(Get-CimInstance Win32_SystemDriver | Where-Object {$_.Name -like '*WinDivert*' -or $_.PathName -like '*WinDivert*'}){throw 'Existing driver'}
$driver=Join-Path $env:ProgramFiles 'IRVIO Exit Driver\WinDivert64.sys'
if((Get-FileHash $driver).Hash -ne '8da085332782708d8767bcace5327a6ec7283c17cfb85e40b03cd2323a90ddc2'){throw 'Wrong pinned driver'}
if((Get-AuthenticodeSignature $driver).Status -ne 'Valid'){throw 'Driver signature invalid'}
New-Item -ItemType Directory $Evidence | Out-Null
Copy-Item $EchoTestBinary (Join-Path $Evidence 'ordinary-echo.test.exe')
Get-FileHash $DriverTestBinary,$EchoTestBinary,$driver | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binaries.json')
function State {
 [ordered]@{
  routes=@(Get-NetRoute | Sort-Object InterfaceIndex,AddressFamily,DestinationPrefix,NextHop | Select-Object InterfaceIndex,AddressFamily,DestinationPrefix,NextHop,RouteMetric)
  dns=@(Get-DnsClientServerAddress | Sort-Object InterfaceIndex,AddressFamily | Select-Object InterfaceIndex,AddressFamily,ServerAddresses)
  adapters=@(Get-NetAdapter -IncludeHidden | Sort-Object InterfaceGuid | Select-Object InterfaceGuid,Name)
 } | ConvertTo-Json -Depth 6 -Compress
}
$before=State
$before | Set-Content (Join-Path $Evidence 'host-before.json')
$password='Lab!'+[Guid]::NewGuid().ToString('N')+'a9'
$createdUser=$false
$createdOwner=$false
$createdDriver=$false
try {
 New-LocalUser $user -Password (ConvertTo-SecureString $password -AsPlainText -Force) -Description 'Disposable IRVIO injected echo oracle' | Out-Null
 $createdUser=$true
 Add-LocalGroupMember -SID 'S-1-5-32-545' -Member $user
 & sc.exe create $owner start= auto binPath= 'C:\Windows\System32\cmd.exe /c exit 0'
 if($LASTEXITCODE){throw 'Owner service creation failed'}
 $createdOwner=$true
 & sc.exe create WinDivert type= kernel start= demand binPath= $driver
 if($LASTEXITCODE){throw 'Driver creation failed'}
 $createdDriver=$true
 & sc.exe start WinDivert
 if($LASTEXITCODE){throw 'Driver start failed'}
 $env:IRVIO_DIVERT_TEST_SERVICE='running'
 $env:IRVIO_ECHO_INJECTION_LAB='1'
 $env:IRVIO_ECHO_PROBE_USER=$user
 $env:IRVIO_ECHO_PROBE_PASSWORD=$password
 $env:IRVIO_ECHO_PROBE_BINARY=Join-Path $Evidence 'ordinary-echo.test.exe'
 $proc=Start-Process $DriverTestBinary -ArgumentList @('-test.v','-test.run=^TestNativeEcho(ForwardInjectionProtected|NonceFilter)$','-test.count=3','-test.shuffle=on','-test.timeout=2m') -Wait -PassThru -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence 'test.log') -RedirectStandardError (Join-Path $Evidence 'stderr.log')
 if($proc.ExitCode){throw "Native echo injection gate failed: $($proc.ExitCode)"}
 foreach($name in @('TestNativeEchoForwardInjectionProtected','TestNativeEchoNonceFilter')) {
  if((Select-String -LiteralPath (Join-Path $Evidence 'test.log') -Pattern ('^--- PASS: '+$name+' ')).Count -ne 3){throw "Missing native repetitions: $name"}
 }
} finally {
 if($createdOwner){
  $env:IRVIO_ECHO_INJECTION_LAB='1'
  $cleanup=Start-Process $DriverTestBinary -ArgumentList @('-test.v','-test.run=^TestNativeEchoInjectionCleanup$','-test.timeout=20s') -Wait -PassThru -NoNewWindow -RedirectStandardOutput (Join-Path $Evidence 'cleanup.log') -RedirectStandardError (Join-Path $Evidence 'cleanup-stderr.log')
  if($cleanup.ExitCode -or !(Select-String -LiteralPath (Join-Path $Evidence 'cleanup.log') -Pattern '^--- PASS: TestNativeEchoInjectionCleanup ' -Quiet)){throw 'WFP cleanup unproven; retain fixture ownership'}
 }
 Remove-Item Env:\IRVIO_ECHO_PROBE_PASSWORD,Env:\IRVIO_ECHO_PROBE_USER,Env:\IRVIO_ECHO_PROBE_BINARY,Env:\IRVIO_ECHO_INJECTION_LAB,Env:\IRVIO_DIVERT_TEST_SERVICE -ErrorAction SilentlyContinue
 $password=$null
 if($createdUser){Remove-LocalUser $user}
 if($createdDriver){
  $s=Get-CimInstance Win32_SystemDriver -Filter "Name='WinDivert'"
  if($s.PathName.Replace('\??\','').Trim('"') -ne $driver){throw 'Unexpected driver target'}
  & sc.exe stop WinDivert
  if($LASTEXITCODE -and $LASTEXITCODE -ne 1062){throw 'Driver stop failed'}
  & sc.exe delete WinDivert
  if($LASTEXITCODE){throw 'Driver removal failed'}
 }
 if($createdOwner){
  & sc.exe delete $owner
  if($LASTEXITCODE){throw 'Owner removal failed'}
 }
 $after=State
 $after | Set-Content (Join-Path $Evidence 'host-after.json')
 if($before -cne $after){throw 'Host networking residue'}
 if(Get-LocalUser $user -ErrorAction SilentlyContinue){throw 'User residue'}
 if(Get-Service $owner,WinDivert -ErrorAction SilentlyContinue){throw 'Service residue'}
}
Get-Content (Join-Path $Evidence 'test.log') -Tail 20
'ECHO INJECTION PRIMITIVE PASS; lease/backend acceptance is checked separately'
