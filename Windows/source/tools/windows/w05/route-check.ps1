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
if(Test-Path Env:IRVIO_ROUTE_NATIVE_TEST){throw 'Unexpected test environment'}
if(!(Test-Path C:\IRVIO-Lab\W05\wintun.dll)){throw 'Signed Wintun DLL missing'}
if(Get-NetAdapter -IncludeHidden | Where-Object Name -eq 'IRVIO-W05-Route'){throw 'Fixture adapter already exists'}
New-Item -ItemType Directory $Evidence | Out-Null
Get-NetRoute | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol,Publish,Store | Export-Clixml ($Evidence+'\baseline-routes.xml')
& cmd.exe /d /c "go vet ./internal/winroute > $Evidence\vet.log 2>&1"
if($LASTEXITCODE){throw 'Vet failed'}
& cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=10 -v ./internal/winroute > $Evidence\unit.log 2>&1"
if($LASTEXITCODE){throw 'Unit tests failed'}
go test -c -gcflags=all=-d=checkptr=2 -o C:\IRVIO-Lab\W05\route.test.exe ./internal/winroute
if($LASTEXITCODE){throw 'Build failed'}
$env:IRVIO_ROUTE_NATIVE_TEST='1'
try{
    & cmd.exe /d /c "C:\IRVIO-Lab\W05\route.test.exe -test.run=^TestNativeSplitRoutes -test.count=3 -test.v -test.timeout=2m > $Evidence\native.log 2>&1"
    $code=$LASTEXITCODE
}finally{Remove-Item Env:IRVIO_ROUTE_NATIVE_TEST}
Get-NetRoute | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol,Publish,Store | Export-Clixml ($Evidence+'\after-routes.xml')
$before=Import-Clixml ($Evidence+'\baseline-routes.xml')
$after=Import-Clixml ($Evidence+'\after-routes.xml')
$diff=Compare-Object $before $after -Property InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol,Publish,Store
if($diff){$diff | Out-String | Set-Content ($Evidence+'\route-diff.txt');throw 'Route baseline differs'}
if(Get-NetAdapter -IncludeHidden | Where-Object Name -eq 'IRVIO-W05-Route'){throw 'Fixture TUN remains'}
Get-Content ($Evidence+'\native.log') -Tail 45
if($code){throw 'Route experiment failed'}
Write-Output 'Native split routes + pinned underlay + route baseline cleanup PASS'
