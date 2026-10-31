# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Helper,
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. $Helper
if(Test-Path -LiteralPath $Evidence){throw 'New evidence directory required'}
New-Item -ItemType Directory $Evidence | Out-Null
Copy-Item -LiteralPath $Payload -Destination "$Evidence\payload" -Recurse
$root="$Evidence\payload"
$path="$root\payload-manifest.json"
$original=[IO.File]::ReadAllText($path)
$results=New-Object 'System.Collections.Generic.List[object]'
function Check-Rejected([string]$Name,[string]$Hash,[bool]$Development,[string]$Expected) {
 $caught=$null
 try {Read-IRVIOPayload $root $Hash $Development | Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong rejection for ${Name}: $caught"}
 $results.Add(@{test=$Name;result='PASS';diagnostic=$caught})
}
$hash=(Get-FileHash $path).Hash
$manifest=Read-IRVIOPayload $root $hash $true
if(-not $manifest.development_unsigned){throw 'Explicit development fixture required'}
$results.Add(@{test='valid-manifest';result='PASS';files=$manifest.files.Count})
Check-Rejected 'manifest-digest' ('0'*64) $true 'manifest hash mismatch'
Check-Rejected 'development-release' $hash $false 'Development payload'
foreach($bad in @('../outside','ui/../outside','ui/a:stream','ui/trailing. /file','ui//double')){
 $manifest=$original | ConvertFrom-Json
 $manifest.files[0].path=$bad
 [IO.File]::WriteAllText($path,($manifest | ConvertTo-Json -Depth 6))
 Check-Rejected "unsafe-$bad" (Get-FileHash $path).Hash $true 'Unsafe payload path'
}
$manifest=$original | ConvertFrom-Json
$manifest.files=@($manifest.files)+@($manifest.files[0])
[IO.File]::WriteAllText($path,($manifest | ConvertTo-Json -Depth 6))
Check-Rejected 'duplicate' (Get-FileHash $path).Hash $true 'Duplicate payload destination'
$manifest=$original | ConvertFrom-Json
$manifest.files=@($manifest.files | Where-Object path -ne 'daemon/irviod.exe')
[IO.File]::WriteAllText($path,($manifest | ConvertTo-Json -Depth 6))
Check-Rejected 'missing-daemon' (Get-FileHash $path).Hash $true 'Missing payload component'
foreach($required in @('ui/msvcp140.dll','ui/vcruntime140.dll','ui/vcruntime140_1.dll','licenses/MSVC-LICENSE.txt')){
 $manifest=$original | ConvertFrom-Json
 $manifest.files=@($manifest.files | Where-Object path -ne $required)
 [IO.File]::WriteAllText($path,($manifest | ConvertTo-Json -Depth 6))
 Check-Rejected "missing-$required" (Get-FileHash $path).Hash $true 'Missing payload component'
}
[IO.File]::WriteAllText($path,$original)
$hash=(Get-FileHash $path).Hash
$target="$root\geo\ATTRIBUTION.md"
[IO.File]::AppendAllText($target,'unexpected change')
Check-Rejected 'changed-file' $hash $true 'hash/size mismatch'
Copy-Item -LiteralPath "$Payload\geo\ATTRIBUTION.md" -Destination $target -Force
$manifest=$original | ConvertFrom-Json
$manifest.files[0].size=-1
[IO.File]::WriteAllText($path,($manifest | ConvertTo-Json -Depth 6))
Check-Rejected 'negative-size' (Get-FileHash $path).Hash $true 'Invalid payload file record'
[IO.File]::WriteAllText($path,$original)
Read-IRVIOPayload $root (Get-FileHash $path).Hash $true | Out-Null
$results | ConvertTo-Json -Depth 5 | Set-Content "$Evidence\results.json"
Get-FileHash $Helper,$PSCommandPath | ConvertTo-Json | Set-Content "$Evidence\inputs.json"
'PAYLOAD READER CHECKS PASS'
