# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use an absolute evidence path without spaces or shell metacharacters'}
if(Test-Path $Evidence){throw 'Evidence exists'}
if(Test-Path Env:IRVIO_SESSION_NATIVE_TEST){throw 'Unexpected native test environment'}
if(!(Test-Path C:\IRVIO-Lab\W05\wintun.dll)){throw 'Verified signed Wintun DLL missing'}
if(Get-NetAdapter -IncludeHidden | Where-Object Name -eq 'IRVIO-Session'){throw 'Fixture TUN already exists'}
New-Item -ItemType Directory $Evidence | Out-Null
Get-NetRoute | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol,Publish,Store | Export-Clixml ($Evidence+'\baseline-routes.xml')
Get-DnsClientServerAddress | Select-Object InterfaceIndex,AddressFamily,ServerAddresses | Export-Clixml ($Evidence+'\baseline-dns.xml')
& cmd.exe /d /c "go vet ./internal/sessioncontrol ./internal/winroute > $Evidence\vet.log 2>&1"
if($LASTEXITCODE){throw 'Vet failed'}
& cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=10 -v ./internal/sessioncontrol ./internal/winroute > $Evidence\unit.log 2>&1"
if($LASTEXITCODE){throw 'Unit tests failed'}
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W05\session.test.exe ./internal/sessioncontrol
if($LASTEXITCODE){throw 'Build failed'}
$env:IRVIO_SESSION_NATIVE_TEST='1'
try{
    & cmd.exe /d /c "C:\IRVIO-Lab\W05\session.test.exe -test.run=^TestNativeSessionTunnel -test.count=3 -test.v -test.timeout=3m > $Evidence\native.log 2>&1"
    $code=$LASTEXITCODE
}finally{Remove-Item Env:IRVIO_SESSION_NATIVE_TEST}
Get-NetRoute | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol,Publish,Store | Export-Clixml ($Evidence+'\after-routes.xml')
Get-DnsClientServerAddress | Select-Object InterfaceIndex,AddressFamily,ServerAddresses | Export-Clixml ($Evidence+'\after-dns.xml')
$diff=Compare-Object (Import-Clixml ($Evidence+'\baseline-routes.xml')) (Import-Clixml ($Evidence+'\after-routes.xml')) -Property InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol,Publish,Store
if($diff){$diff | Out-String | Set-Content ($Evidence+'\route-diff.txt');throw 'Route baseline differs'}
$diff=Compare-Object (Import-Clixml ($Evidence+'\baseline-dns.xml')) (Import-Clixml ($Evidence+'\after-dns.xml')) -Property InterfaceIndex,AddressFamily,ServerAddresses
if($diff){$diff | Out-String | Set-Content ($Evidence+'\dns-diff.txt');throw 'DNS baseline differs'}
if(Get-NetAdapter -IncludeHidden | Where-Object Name -eq 'IRVIO-Session'){throw 'Fixture TUN remains'}
Get-Content ($Evidence+'\native.log') -Tail 65
if($code){throw 'Session control gate failed; evidence retained'}
Write-Output 'Session control sockets and cleanup gates PASS'
