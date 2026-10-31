# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Binary,[Parameter(Mandatory=$true)][string]$Evidence,[switch]$NetworkBind)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Binary -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$' -or $Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Absolute lab paths without shell metacharacters required'}
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(Get-Service IRVIO-W05-Owner -ErrorAction SilentlyContinue){throw 'WFP fixture already exists'}
if(Get-NetAdapter -IncludeHidden | Where-Object Name -eq 'IRVIO-DP'){throw 'TUN fixture already exists'}
New-Item -ItemType Directory $Evidence | Out-Null
Get-NetRoute | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol,Publish,Store | Export-Clixml "$Evidence\routes-before.xml"
Get-DnsClientServerAddress | Select-Object InterfaceIndex,AddressFamily,ServerAddresses | Export-Clixml "$Evidence\dns-before.xml"
$created=$false
try{
    New-Service -Name IRVIO-W05-Owner -BinaryPathName 'C:\IRVIO-Lab\W05\filterprobe.exe service' -StartupType Automatic | Out-Null
    $created=$true
    Start-Service IRVIO-W05-Owner
    if($NetworkBind){
        if(-not $env:IRVIO_DP_PEER_LISTEN -or -not $env:IRVIO_DP_PEER_NONCE){throw 'Linux peer fixture endpoint and nonce required'}
        $env:IRVIO_CLIENT_DP_NETWORK_BIND='1'
        $test='^TestWindowsNativeClientDataPlaneNetworkBind$';$count=1
    }else{
        $env:IRVIO_CLIENT_DP_NATIVE='1'
        $test='^TestWindowsNativeClientDataPlane$';$count=3
    }
    & cmd.exe /d /c "`"$Binary`" -test.run=$test -test.v -test.count=$count -test.timeout=5m > `"$Evidence\native.log`" 2>&1"
    $code=$LASTEXITCODE
}finally{
    Remove-Item Env:IRVIO_CLIENT_DP_NATIVE -ErrorAction SilentlyContinue
    Remove-Item Env:IRVIO_CLIENT_DP_NETWORK_BIND -ErrorAction SilentlyContinue
    $env:IRVIO_CLIENT_DP_CLEANUP='1'
    & cmd.exe /d /c "`"$Binary`" -test.run=^TestWindowsNativeClientDPCleanup$ -test.v > `"$Evidence\cleanup.log`" 2>&1"
    $cleanup=$LASTEXITCODE
    Remove-Item Env:IRVIO_CLIENT_DP_CLEANUP
    if($cleanup){throw 'WFP cleanup failed; retain owner service for recovery'}
    if($created){Stop-Service IRVIO-W05-Owner;sc.exe delete IRVIO-W05-Owner;if($LASTEXITCODE){throw 'fixture deletion failed'}}
}
Get-NetRoute | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol,Publish,Store | Export-Clixml "$Evidence\routes-after.xml"
Get-DnsClientServerAddress | Select-Object InterfaceIndex,AddressFamily,ServerAddresses | Export-Clixml "$Evidence\dns-after.xml"
$routeDiff=Compare-Object (Import-Clixml "$Evidence\routes-before.xml") (Import-Clixml "$Evidence\routes-after.xml") -Property InterfaceIndex,DestinationPrefix,NextHop,RouteMetric,Protocol,Publish,Store
$dnsDiff=Compare-Object (Import-Clixml "$Evidence\dns-before.xml") (Import-Clixml "$Evidence\dns-after.xml") -Property InterfaceIndex,AddressFamily,ServerAddresses
Get-Content "$Evidence\native.log" -Tail 50
if($routeDiff -or $dnsDiff){$routeDiff,$dnsDiff | Out-String | Set-Content "$Evidence\state-diff.txt";throw 'host state differs from baseline'}
if(Get-NetAdapter -IncludeHidden | Where-Object Name -eq 'IRVIO-DP'){throw 'TUN residue'}
if($code){throw 'Native SessionInstaller gate failed'}
if($NetworkBind){'NATIVE SESSION INSTALLER NETWORK BIND PASS; synthetic ICE fixture, not discovery/Exit NAT proof'}
else{'NATIVE SESSION INSTALLER PASS; test BIND callback, not an end-to-end network lease proof'}
