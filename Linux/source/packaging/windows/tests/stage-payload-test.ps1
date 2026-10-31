# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$StageScript,
 [Parameter(Mandatory=$true)][string]$Daemon,
 [Parameter(Mandatory=$true)][string]$Wintun,
 [Parameter(Mandatory=$true)][string]$DriverDirectory,
 [Parameter(Mandatory=$true)][string]$MSVCRuntimeDirectory,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if(Test-Path -LiteralPath $Evidence){throw 'New evidence directory required'}
New-Item -ItemType Directory $Evidence | Out-Null
$fixture=Join-Path $Evidence 'fixture'
$ui=Join-Path $fixture 'ui'
$licenses=Join-Path $fixture 'licenses'
$driver=Join-Path $fixture 'driver'
New-Item -ItemType Directory "$ui\data\flutter_assets",$licenses,$driver | Out-Null
Copy-Item $Daemon "$fixture\irviod.exe"
Copy-Item $Wintun "$fixture\wintun.dll"
Copy-Item "$DriverDirectory\WinDivert.dll","$DriverDirectory\WinDivert64.sys" $driver
foreach($name in @('irvio.exe','irvio-ui-bridge.exe','flutter_windows.dll')){Copy-Item $Daemon "$ui\$name"}
foreach($name in @('msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll')){Copy-Item (Join-Path $MSVCRuntimeDirectory $name) "$ui\$name"}
foreach($name in @('data\icudtl.dat','data\app.so','data\flutter_assets\NOTICES.Z')){[IO.File]::WriteAllText("$ui\$name",'packaging fixture, not a runnable Flutter bundle')}
[IO.File]::WriteAllText("$ui\native_assets.json",'{"format-version":[1,0,0],"native-assets":{}}')
foreach($name in @('AGPL-3.0-only.txt','WINTUN-LICENSE.txt','WINDIVERT-LICENSE.txt','GO-MODULES.txt','FLUTTER-LICENSE.txt','MSVC-LICENSE.txt')){[IO.File]::WriteAllText("$licenses\$name",'packaging fixture, not release license evidence')}
[IO.File]::WriteAllText("$fixture\country.mmdb",'fixture; MMDB semantic validation belongs to release build')
[IO.File]::WriteAllText("$fixture\ATTRIBUTION.md",'fixture')
$arguments=@{
 Daemon="$fixture\irviod.exe";UIBundle=$ui;Wintun="$fixture\wintun.dll";WinDivertDirectory=$driver
 GeoIP="$fixture\country.mmdb";GeoAttribution="$fixture\ATTRIBUTION.md";Licenses=$licenses
 SourceRevision=('a'*40);Version='1.1.0-test';Output="$Evidence\payload";DevelopmentUnsigned=$true
}
$results=New-Object 'System.Collections.Generic.List[object]'
function Reject([string]$Name,[hashtable]$Overrides,[string]$Expected) {
 $argsCopy=$arguments.Clone()
 $argsCopy.Output=Join-Path $Evidence $Name
 foreach($key in $Overrides.Keys){$argsCopy[$key]=$Overrides[$key]}
 $caught=$null
 try {& $StageScript @argsCopy | Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong result for ${Name}: $caught"}
 if((Test-Path $argsCopy.Output) -and $Name -ne 'existing-output'){throw "Rejected input left an output directory: $Name"}
 $results.Add(@{test=$Name;result='PASS';diagnostic=$caught})
}
Reject 'invalid-revision' @{SourceRevision='HEAD'} 'Full source commit'
Reject 'invalid-version' @{Version='../bad'} 'Invalid package version'
New-Item -ItemType Directory "$Evidence\existing-output" | Out-Null
Reject 'existing-output' @{} 'must not exist'
Rename-Item "$ui\irvio-ui-bridge.exe" 'bridge.exe'
Reject 'unexpected-exe' @{} 'Unexpected UI file'
Rename-Item "$ui\bridge.exe" 'irvio-ui-bridge.exe'
[IO.File]::WriteAllText("$ui\debug.pdb",'fixture')
Reject 'pdb-residue' @{} 'Unexpected UI file'
Move-Item "$ui\debug.pdb" "$fixture\debug.pdb"
[IO.File]::WriteAllText("$ui\data\flutter_assets\identity.key",'fixture')
Reject 'private-material' @{} 'Forbidden UI payload'
Move-Item "$ui\data\flutter_assets\identity.key" "$fixture\identity.key"
Rename-Item "$licenses\GO-MODULES.txt" 'modules.txt'
Reject 'missing-notice' @{} 'Missing dependency notice'
Rename-Item "$licenses\modules.txt" 'GO-MODULES.txt'
Move-Item "$ui\msvcp140.dll" "$fixture\msvcp140.dll"
Reject 'missing-msvc-runtime' @{} 'Missing release UI component: ui/msvcp140.dll'
Copy-Item $Daemon "$ui\msvcp140.dll"
Reject 'unsigned-msvc-runtime' @{} 'Authenticode validation failed'
Copy-Item "$fixture\msvcp140.dll" "$ui\msvcp140.dll" -Force
Move-Item "$licenses\MSVC-LICENSE.txt" "$fixture\MSVC-LICENSE.txt"
Reject 'missing-msvc-notice' @{} 'Missing dependency notice: MSVC-LICENSE.txt'
Move-Item "$fixture\MSVC-LICENSE.txt" "$licenses\MSVC-LICENSE.txt"
Reject 'unapproved-wintun' @{Wintun=$Daemon} 'Pinned dependency hash mismatch'
$bad=Join-Path $fixture 'x86.exe'
$bytes=[IO.File]::ReadAllBytes($Daemon)
$pe=[BitConverter]::ToInt32($bytes,0x3c)
$bytes[$pe+4]=0x4c
$bytes[$pe+5]=0x01
[IO.File]::WriteAllBytes($bad,$bytes)
Reject 'wrong-architecture' @{Daemon=$bad} 'Not an AMD64 PE'
New-Item -ItemType Junction -Path "$fixture\linked-ui" -Target $ui | Out-Null
Reject 'reparse-input' @{UIBundle="$fixture\linked-ui"} 'Reparse point'
New-Item -ItemType Junction -Path "$ui\data\flutter_assets\linked" -Target $licenses | Out-Null
Reject 'nested-reparse-input' @{} 'Reparse point'
[IO.Directory]::Delete("$ui\data\flutter_assets\linked")
if((Get-AuthenticodeSignature $Daemon).Status -eq 'NotSigned'){
 Reject 'unsigned-release' @{DevelopmentUnsigned=$false} 'Authenticode validation failed'
}else{throw 'Fixture needs an unsigned development daemon to test release rejection'}
& $StageScript @arguments | Set-Content "$Evidence\stage.log"
$manifest=Get-Content -Raw "$Evidence\payload\payload-manifest.json" | ConvertFrom-Json
if(-not $manifest.development_unsigned -or $manifest.platform -ne 'windows-amd64'){throw 'Manifest scope is incorrect'}
$actual=@(Get-ChildItem "$Evidence\payload" -Recurse -File | Where-Object Name -ne 'payload-manifest.json')
if($actual.Count -ne $manifest.files.Count){throw 'Unmanifested payload files'}
foreach($file in $manifest.files){
 if($file.path -match ':|\\|\.\.' -or $file.PSObject.Properties.Name -contains 'source'){throw 'Unsafe or absolute manifest path'}
 $path=Join-Path "$Evidence\payload" $file.path
 if((Get-Item $path).Length -ne $file.size -or (Get-FileHash $path).Hash.ToLowerInvariant() -ne $file.sha256){throw 'Manifest/file mismatch'}
}
$results.Add(@{test='development-payload-hash-coverage';result='PASS';files=$actual.Count})
$results | ConvertTo-Json -Depth 5 | Set-Content "$Evidence\results.json"
Get-FileHash $StageScript,$PSCommandPath,$Daemon,$Wintun | ConvertTo-Json | Set-Content "$Evidence\inputs.json"
'PAYLOAD FIXTURE PASS (not a release or installer acceptance)'
