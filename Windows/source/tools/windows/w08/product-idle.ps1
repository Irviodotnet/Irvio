# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Binary,
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [Parameter(Mandatory=$true)][string]$DesktopSID,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [string]$UISmoke
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(-not [Security.Principal.WindowsIdentity]::GetCurrent().IsSystem){throw 'Run through the disposable VM SYSTEM harness'}
if($DesktopSID -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$'){throw 'Explicit local desktop account SID required'}
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$' -or (Test-Path $Evidence)){throw 'New absolute evidence directory required'}
$root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO'
$bin=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO-W08'
if((Test-Path $root) -or (Test-Path $bin) -or (Get-Service IRVIO,IRVIO-Recovery -ErrorAction SilentlyContinue)){throw 'Existing product installation; refusing overwrite'}
function Host-State {
 [pscustomobject]@{
  routes=@(Get-NetRoute | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"} | Sort-Object)
  dns=@(Get-DnsClientServerAddress | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
  adapters=@(Get-NetAdapter -IncludeHidden | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
 }
}
function Native-Probe([string]$Mode,[string]$Name) {
 $env:IRVIO_WINDOWS_PRODUCT_IDLE=$Mode
 & $TestBinary '-test.run=^TestWindowsNativeProductIdle$' '-test.v' '-test.timeout=45s' *> (Join-Path $Evidence "$Name.log")
 if($LASTEXITCODE){throw "Native probe failed: $Name"}
 if((Get-Content -Raw (Join-Path $Evidence "$Name.log")) -notmatch '--- PASS: TestWindowsNativeProductIdle '){throw 'Native probe skipped'}
}
$before=Host-State
New-Item -ItemType Directory $Evidence | Out-Null
$before | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'host-before.json')
Get-FileHash $Binary,$TestBinary | ConvertTo-Json | Set-Content (Join-Path $Evidence 'binaries.json')
$createdService=$false
$createdRecovery=$false
try {
 New-Item -ItemType Directory $root,$bin | Out-Null
 foreach($directory in @($root,$bin)) {
  $acl=New-Object System.Security.AccessControl.DirectorySecurity
  $acl.SetSecurityDescriptorSddlForm('O:SYG:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)')
  Set-Acl -LiteralPath $directory -AclObject $acl
 }
 Copy-Item $Binary (Join-Path $bin 'irviod.exe')
 [IO.File]::WriteAllText((Join-Path $root 'desktop-user.sid'),$DesktopSID,[Text.Encoding]::ASCII)
 $acl=New-Object System.Security.AccessControl.FileSecurity
 $acl.SetSecurityDescriptorSddlForm('O:SYG:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)')
 Set-Acl -LiteralPath (Join-Path $root 'desktop-user.sid') -AclObject $acl
 $command='"'+(Join-Path $bin 'irviod.exe')+'"'
 New-Service -Name IRVIO-Recovery -BinaryPathName ($command+' --boot-recover') -StartupType Automatic | Out-Null
 $createdRecovery=$true
 New-Service -Name IRVIO -BinaryPathName $command -StartupType Manual | Out-Null
 $createdService=$true
 & sc.exe failure IRVIO reset= 86400 actions= restart/1000/restart/3000
 if($LASTEXITCODE){throw 'Failure actions configuration failed'}
 & sc.exe failureflag IRVIO 1
 if($LASTEXITCODE){throw 'Failure flag configuration failed'}
 if($UISmoke){
  & $UISmoke *> (Join-Path $Evidence 'dart-bridge-idle.log')
  if($LASTEXITCODE -or (Get-Content -Raw (Join-Path $Evidence 'dart-bridge-idle.log')) -notmatch 'API=1.7 state=disconnected'){throw 'Dart/bridge product idle probe failed'}
 } else { Start-Service IRVIO }
 Native-Probe 'status' 'system-idle'
 $first=(Get-CimInstance Win32_Service -Filter "Name='IRVIO'").ProcessId
 Native-Probe 'reset' 'profile-reset'
 if((Get-CimInstance Win32_Service -Filter "Name='IRVIO'").ProcessId -ne $first){throw 'Profile reset replaced SCM process'}
 Stop-Process -Id $first -Force
 $deadline=(Get-Date).AddSeconds(30)
 $next=0
 while((Get-Date) -lt $deadline){
  $service=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
  if($service.State -eq 'Running' -and $service.ProcessId -gt 0 -and $service.ProcessId -ne $first){$next=$service.ProcessId;break}
  Start-Sleep -Milliseconds 200
 }
 if(-not $next){throw 'SCM did not restart crashed idle daemon'}
 Native-Probe 'shutdown' 'after-idle-crash'
 Start-Sleep -Seconds 5
 $service=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
 if($service.State -ne 'Stopped' -or $service.ExitCode -ne 0){throw 'Intentional shutdown failed or restarted'}
 @{old_pid=$first;new_pid=$next;final_state=$service.State;exit_code=$service.ExitCode} | ConvertTo-Json | Set-Content (Join-Path $Evidence 'scm.json')
} finally {
 Remove-Item Env:IRVIO_WINDOWS_PRODUCT_IDLE -ErrorAction SilentlyContinue
 if($createdService -and (Get-Service IRVIO).Status -ne 'Stopped'){Stop-Service IRVIO}
 $env:IRVIO_WINDOWS_PRODUCT_IDLE_CLEANUP='1'
 & $TestBinary '-test.run=^TestWindowsNativeProductIdleCleanup$' '-test.v' *> (Join-Path $Evidence 'cleanup.log')
 $cleanup=$LASTEXITCODE
 Remove-Item Env:IRVIO_WINDOWS_PRODUCT_IDLE_CLEANUP
 if($cleanup -or (Get-Content -Raw (Join-Path $Evidence 'cleanup.log')) -notmatch '--- PASS: TestWindowsNativeProductIdleCleanup '){throw 'Cleanup not proven; retain recovery installation'}
 foreach($name in @('IRVIO','IRVIO-Recovery')) {
  if(($name -eq 'IRVIO' -and $createdService) -or ($name -eq 'IRVIO-Recovery' -and $createdRecovery)){
   & sc.exe delete $name
   if($LASTEXITCODE){throw "Service deletion failed: $name"}
  }
 }
 Move-Item -LiteralPath $root -Destination (Join-Path $Evidence 'profile-state')
 Move-Item -LiteralPath $bin -Destination (Join-Path $Evidence 'installed-binary')
 $after=Host-State
 $delta=[pscustomobject]@{routes=@(Compare-Object $before.routes $after.routes);dns=@(Compare-Object $before.dns $after.dns);adapters=@(Compare-Object $before.adapters $after.adapters)}
 $delta | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'host-cleanup.json')
 if($delta.routes.Count -or $delta.dns.Count -or $delta.adapters.Count){throw 'Host state residue'}
}
'PRODUCT IDLE / RESET / IDLE CRASH / SHUTDOWN / CLEANUP PASS'
