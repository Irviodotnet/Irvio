# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][ValidateSet('Connect','Crash','PrepareReboot','Reboot','VerifyReboot')][string]$Mode,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [string]$Binary,
 [string]$DesktopSID,
 [string]$BootstrapFile,
 [string]$Country,
 [string]$ExpectedEgress,
 [string]$Wintun,
 [string]$GeoIP,
 [string]$Attribution,
 [string]$WANProbe
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(-not [Security.Principal.WindowsIdentity]::GetCurrent().IsSystem){throw 'Disposable Windows VM SYSTEM harness only'}
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Explicit absolute evidence directory required'}
$root='C:\ProgramData\IRVIO'
$bin='C:\Program Files\IRVIO-W08'
$command='"C:\Program Files\IRVIO-W08\irviod.exe"'
$driver='C:\Program Files\IRVIO Exit Driver\WinDivert64.sys'
function Host-State {
 $adapters=@(Get-NetAdapter -IncludeHidden)
 $routes=@(Get-NetRoute)
 $dns=@(Get-DnsClientServerAddress)
 [pscustomobject]@{
  routes=@($routes | ForEach-Object {"$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$([uint32]$_.Protocol)"} | Sort-Object)
  dns=@($dns | ForEach-Object {"$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
  adapters=@($adapters | Select-Object -ExpandProperty InterfaceGuid | Sort-Object)
  bootRoutes=@($routes | Where-Object {$_.InterfaceAlias -ne 'vEthernet (Default Switch)'} | ForEach-Object {"$($_.InterfaceAlias)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$([uint32]$_.Protocol)"} | Sort-Object)
  bootDNS=@($dns | ForEach-Object {"$($_.InterfaceAlias)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"} | Sort-Object)
  bootAdapters=@($adapters | ForEach-Object {
   $id=$_.InterfaceGuid
   if(($_.Name -eq 'vEthernet (Default Switch)' -and $_.InterfaceDescription -eq 'Hyper-V Virtual Ethernet Adapter') -or ($_.Name -eq 'vSwitch (Default Switch)' -and $_.InterfaceDescription -eq 'Hyper-V Virtual Switch Extension Adapter')){$id='OS-recreated-default-switch'}
   "$($_.Name)|$($_.InterfaceDescription)|$id"
  } | Sort-Object)
  adapterDetails=@($adapters | Select-Object Name,InterfaceDescription,InterfaceGuid,ifIndex)
  routeDetails=@($routes | Select-Object InterfaceAlias,InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol)
 }
}
function Assert-Fixture {
 $service=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
 $recovery=Get-CimInstance Win32_Service -Filter "Name='IRVIO-Recovery'"
 $kernel=Get-CimInstance Win32_SystemDriver -Filter "Name='WinDivert'"
 if($service.PathName -ne $command -or $recovery.PathName -ne "$command --boot-recover" -or $kernel.PathName -notin @($driver,('\??\'+$driver))){throw 'Not the owned disposable fixture'}
}
function Native-Probe([string]$Test,[string]$Name,[string]$Timeout='4m') {
 $path=Join-Path $Evidence "$Name.log"
 if(Test-Path $path){throw "Do not overwrite evidence: $path"}
 & $TestBinary "-test.run=^$Test`$" '-test.v' "-test.timeout=$Timeout" *> $path
 $result=$LASTEXITCODE
 Get-Content $path -Tail 12
 if($result -or (Get-Content -Raw $path) -notmatch "--- PASS: $Test "){throw "Native gate failed or skipped: $Name"}
}
function Assert-Clean {
 $env:IRVIO_WINDOWS_PRODUCT_IDLE_CLEANUP='1'
 Native-Probe 'TestWindowsNativeProductIdleCleanup' 'natural-cleanup' '30s'
 $before=Get-Content -Raw (Join-Path $Evidence 'host-before.json') | ConvertFrom-Json
 $after=Host-State
 $after | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'host-after.json')
 $delta=@{routes=@(Compare-Object $before.routes $after.routes);dns=@(Compare-Object $before.dns $after.dns);adapters=@(Compare-Object $before.adapters $after.adapters)}
 $delta | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'natural-host-cleanup.json')
 if($Mode -eq 'VerifyReboot'){
  $delta=@{routes=@(Compare-Object $before.bootRoutes $after.bootRoutes);dns=@(Compare-Object $before.bootDNS $after.bootDNS);adapters=@(Compare-Object $before.bootAdapters $after.bootAdapters)}
  $delta | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'boot-normalized-host-cleanup.json')
  'Reboot comparison uses interface names/GUIDs; OS-owned Hyper-V Default Switch GUID/subnet recreation is recorded, not asserted identical.'
 }
 if($delta.routes.Count -or $delta.dns.Count -or $delta.adapters.Count){throw 'Host residue; installation retained'}
}
function Archive-Fixture {
 Assert-Fixture
 foreach($name in @('IRVIO','IRVIO-Recovery')){if((Get-Service $name).Status -ne 'Stopped'){throw "Service still running: $name"}}
 if((Get-Service WinDivert).Status -ne 'Stopped'){
  & sc.exe stop WinDivert
  if($LASTEXITCODE){throw 'Driver stop failed'}
 }
 foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){& sc.exe delete $name;if($LASTEXITCODE){throw "Service removal failed: $name"}}
 Move-Item -LiteralPath $root -Destination (Join-Path $Evidence 'profile-state')
 Move-Item -LiteralPath $bin -Destination (Join-Path $Evidence 'installed-binary')
}
if($Mode -eq 'Reboot'){
 Assert-Fixture
 if(-not(Test-Path (Join-Path $Evidence 'prepared-for-reboot.json'))){throw 'No successfully prepared connected session'}
 $service=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
 if($service.State -ne 'Running'){throw 'Product not running'}
 & sc.exe failure IRVIO reset= 0 actions= """"
 if($LASTEXITCODE){throw 'Cannot disable crash restart'}
 Stop-Process -Id $service.ProcessId -Force
 (Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(15))
 if(-not(Test-Path "$root\runtime\session-state")){throw 'Missing durable crash intent'}
 Restart-Computer -Force
 return
}
if($Mode -eq 'VerifyReboot'){
 Assert-Fixture
 $before=Get-Content -Raw (Join-Path $Evidence 'prepared-for-reboot.json') | ConvertFrom-Json
 $now=(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')
 if($before.boot -eq $now){throw 'Real boot has not changed'}
 @{before=$before.boot;after=$now} | ConvertTo-Json | Set-Content (Join-Path $Evidence 'real-reboot.json')
 (Get-Service IRVIO-Recovery).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(60))
 $env:IRVIO_WINDOWS_PRODUCT_REBOOT='verify-real-reboot'
 $env:IRVIO_WINDOWS_PRODUCT_WAN_PROBE=$WANProbe
 Native-Probe 'TestWindowsNativeProductPostReboot' 'post-reboot' '60s'
 (Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))
 Assert-Clean
 Archive-Fixture
 'REAL REBOOT / NO AUTO-CONNECT / ORDINARY WAN / CLEANUP PASS'
 return
}
if((Test-Path $Evidence) -or (Test-Path $root) -or (Test-Path $bin) -or (Get-Service IRVIO,IRVIO-Recovery,WinDivert -ErrorAction SilentlyContinue)){throw 'Existing evidence or installation; refusing overwrite'}
if($DesktopSID -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$' -or $Country -notmatch '^[A-Z]{2}$'){throw 'Explicit local user SID and country required'}
foreach($path in @($Binary,$TestBinary,$BootstrapFile,$Wintun,$GeoIP,$Attribution,$driver)){if(-not $path -or -not(Test-Path -LiteralPath $path -PathType Leaf)){throw "Missing required payload: $path"}}
if($Mode -eq 'Crash' -and -not $WANProbe){throw 'Explicit reachable ordinary WAN probe required'}
if(-not $ExpectedEgress){throw 'Explicit expected Exit egress address required'}
New-Item -ItemType Directory $Evidence | Out-Null
Host-State | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $Evidence 'host-before.json')
Get-FileHash $Binary,$TestBinary,$Wintun,$GeoIP,$driver | ConvertTo-Json | Set-Content (Join-Path $Evidence 'payload-hashes.json')
New-Item -ItemType Directory $root,$bin | Out-Null
foreach($directory in @($root,$bin)){
 $acl=New-Object System.Security.AccessControl.DirectorySecurity
 $acl.SetSecurityDescriptorSddlForm('O:SYG:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)')
 Set-Acl -LiteralPath $directory -AclObject $acl
}
Copy-Item $Binary "$bin\irviod.exe"
Copy-Item $Wintun "$bin\wintun.dll"
New-Item -ItemType Directory "$root\geo" | Out-Null
Copy-Item $GeoIP "$root\geo\country.mmdb"
Copy-Item $Attribution "$root\geo\ATTRIBUTION.md"
[IO.File]::WriteAllText("$root\desktop-user.sid",$DesktopSID,[Text.Encoding]::ASCII)
$acl=New-Object System.Security.AccessControl.FileSecurity
$acl.SetSecurityDescriptorSddlForm('O:SYG:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)')
Set-Acl -LiteralPath "$root\desktop-user.sid" -AclObject $acl
New-Service IRVIO-Recovery -BinaryPathName "$command --boot-recover" -StartupType Automatic | Out-Null
New-Service IRVIO -BinaryPathName $command -StartupType Manual | Out-Null
& sc.exe failure IRVIO reset= 86400 actions= restart/1000/restart/3000
if($LASTEXITCODE){throw 'Failure actions failed'}
& sc.exe failureflag IRVIO 1
if($LASTEXITCODE){throw 'Failure flag failed'}
& sc.exe create WinDivert type= kernel start= demand binPath= $driver
if($LASTEXITCODE){throw 'Driver creation failed'}
& sc.exe config IRVIO depend= WinDivert
if($LASTEXITCODE){throw 'Driver dependency configuration failed'}
& sc.exe start WinDivert
if($LASTEXITCODE){throw 'Driver startup failed'}
Start-Service IRVIO
$env:IRVIO_WINDOWS_PRODUCT_IDLE='status'
Native-Probe 'TestWindowsNativeProductIdle' 'initial-idle' '45s'
$env:IRVIO_WINDOWS_PRODUCT_BOOTSTRAP_FILE=$BootstrapFile
$env:IRVIO_WINDOWS_PRODUCT_COUNTRY=$Country
$env:IRVIO_WINDOWS_PRODUCT_EXPECTED_EGRESS=$ExpectedEgress
$env:IRVIO_WINDOWS_PRODUCT_CRASH=if($Mode -eq 'Crash'){'1'}else{'0'}
$env:IRVIO_WINDOWS_PRODUCT_WAN_PROBE=$WANProbe
$env:IRVIO_WINDOWS_PRODUCT_REBOOT=if($Mode -eq 'PrepareReboot'){'leave-connected-for-real-reboot'}else{''}
# Failures deliberately retain the exact installation for out-of-band diagnosis and scoped cleanup.
Native-Probe 'TestWindowsNativeProductGlobalConnection' 'global'
if($Mode -eq 'PrepareReboot'){
 Copy-Item "$root\runtime\session-state" (Join-Path $Evidence 'intent-before-reboot.json')
 @{boot=(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')} | ConvertTo-Json | Set-Content (Join-Path $Evidence 'prepared-for-reboot.json')
 'CONNECTED; invoke Reboot followed by VerifyReboot through out-of-band management'
 return
}
(Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))
Assert-Clean
Archive-Fixture
'GLOBAL VPN / USER TRAFFIC / CLEANUP PASS'
