# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][switch]$DisposableVM,
 [ValidateSet('Integration','Full')][string]$Scope='Integration'
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(-not $DisposableVM){throw 'Explicit disposable Windows VM required'}
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$' -or (Test-Path $Evidence)){throw 'New absolute evidence directory required'}
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
$principal=New-Object Security.Principal.WindowsPrincipal($identity)
if(-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Elevated loopback fixture required'}
if((go env GOOS) -ne 'windows'){throw 'Native Windows Go required'}
$loop=@(Get-NetIPAddress -AddressFamily IPv4 -IPAddress '127.0.0.1')
if($loop.Count -ne 1){throw 'Unique Windows loopback interface required'}
$index=$loop[0].InterfaceIndex
$aliases=@('127.0.0.2','127.0.0.3')
foreach($address in $aliases){
 if(Get-NetIPAddress -IPAddress $address -ErrorAction SilentlyContinue){throw "Fixture address already configured: $address"}
}
function Loopback-State {
 @(
  Get-NetIPAddress -InterfaceIndex $index|ForEach-Object {"address|$($_.IPAddress)|$($_.PrefixLength)|$($_.SkipAsSource)"}
  Get-NetRoute -InterfaceIndex $index|ForEach-Object {"route|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$($_.Protocol)"}
 )|Sort-Object
}
$before=@(Loopback-State)
$created=@()
New-Item -ItemType Directory $Evidence|Out-Null
$before|ConvertTo-Json|Set-Content "$Evidence\loopback-before.json"
$code=$null
try {
 foreach($address in $aliases){
  New-NetIPAddress -InterfaceIndex $index -IPAddress $address -PrefixLength 32 -AddressFamily IPv4 -PolicyStore ActiveStore -SkipAsSource $true|Out-Null
  $created+=$address
 }
 Get-NetIPAddress -InterfaceIndex $index|Select-Object IPAddress,PrefixLength,AddressState,SkipAsSource|ConvertTo-Json|Set-Content "$Evidence\fixture.json"
 $gates=@(@{name='integration';command='go test -race -shuffle=on -count=10 -timeout=300s -v ./test/integration'})
 if($Scope -eq 'Full'){
  $gates=@(
   @{name='vet';command='go vet ./...'},
   @{name='test';command='go test -timeout=300s ./...'},
   @{name='shuffle';command='go test -shuffle=on -count=10 -timeout=600s ./...'},
   @{name='race';command='go test -race -timeout=600s ./...'},
   @{name='race-shuffle';command='go test -race -shuffle=on -count=5 -timeout=900s ./...'},
   @{name='build';command="go build -o $Evidence\irviod.exe ./cmd/irviod"},
   @{name='trimpath';command="go build -trimpath -o $Evidence\irviod-trimpath.exe ./cmd/irviod"}
  )
 }
 $results=@()
 $code=0
 foreach($gate in $gates){
  $watch=[Diagnostics.Stopwatch]::StartNew()
  @{stage=$gate.name;state='started';at=[DateTime]::UtcNow.ToString('o')}|ConvertTo-Json -Compress|Add-Content "$Evidence\events.ndjson"
  & cmd.exe /d /c "$($gate.command) > $Evidence\$($gate.name).log 2>&1"
  $gateCode=$LASTEXITCODE
  $watch.Stop()
  if($gateCode){$code=1}
  $results+=@{stage=$gate.name;exit_code=$gateCode;duration_ms=$watch.ElapsedMilliseconds}
  $results|ConvertTo-Json|Set-Content "$Evidence\stages.json"
  @{stage=$gate.name;state='finished';exit_code=$gateCode;at=[DateTime]::UtcNow.ToString('o')}|ConvertTo-Json -Compress|Add-Content "$Evidence\events.ndjson"
  Get-Content "$Evidence\$($gate.name).log" -Tail 5
 }
 if($code){throw 'Native gates failed; evidence retained'}
 if($Scope -eq 'Full'){Get-FileHash "$Evidence\irviod.exe","$Evidence\irviod-trimpath.exe"|ConvertTo-Json|Set-Content "$Evidence\binaries.json"}
} catch {
 $_|Out-String|Set-Content "$Evidence\failure.txt"
 throw
} finally {
 foreach($address in $created){
  $owned=Get-NetIPAddress -InterfaceIndex $index -IPAddress $address -AddressFamily IPv4 -PolicyStore ActiveStore
  if($owned.PrefixLength -ne 32 -or -not $owned.SkipAsSource){throw 'Fixture ownership changed; refusing removal'}
  $owned|Remove-NetIPAddress -Confirm:$false
 }
 $after=@(Loopback-State)
 $after|ConvertTo-Json|Set-Content "$Evidence\loopback-after.json"
 $delta=@(Compare-Object $before $after)
 @{test_exit_code=$code;cleanup_delta=$delta}|ConvertTo-Json -Depth 6|Set-Content "$Evidence\result.json"
 if($delta.Count){throw 'Loopback fixture cleanup differs from baseline'}
}
