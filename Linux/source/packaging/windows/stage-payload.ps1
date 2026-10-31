# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$Daemon,
 [Parameter(Mandatory=$true)][string]$UIBundle,
 [Parameter(Mandatory=$true)][string]$Wintun,
 [Parameter(Mandatory=$true)][string]$WinDivertDirectory,
 [Parameter(Mandatory=$true)][string]$GeoIP,
 [Parameter(Mandatory=$true)][string]$GeoAttribution,
 [Parameter(Mandatory=$true)][string]$Licenses,
 [Parameter(Mandatory=$true)][string]$SourceRevision,
 [Parameter(Mandatory=$true)][string]$Version,
 [Parameter(Mandatory=$true)][string]$Output,
 [switch]$DevelopmentUnsigned
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
if($env:OS -ne 'Windows_NT' -or -not [Environment]::Is64BitProcess){throw 'Use native 64-bit Windows PowerShell'}
if($SourceRevision -notmatch '^[a-f0-9]{40}$'){throw 'Full source commit required'}
if($Version -notmatch '^\d+\.\d+\.\d+(?:[-.][A-Za-z0-9]+)*$'){throw 'Invalid package version'}
$Output=[IO.Path]::GetFullPath($Output)
if(Test-Path -LiteralPath $Output){throw 'Output must not exist; never overwrite a previous payload'}

function Assert-PlainPath([string]$Path) {
 $item=Get-Item -LiteralPath $Path -Force
 while($null -ne $item){
  if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw "Reparse point in input/output path: $Path"}
  if($item -is [IO.FileInfo]){$item=$item.Directory}else{$item=$item.Parent}
 }
}
function Get-PlainTree([string]$Directory) {
 foreach($entry in Get-ChildItem -LiteralPath $Directory -Force){
  Assert-PlainPath $entry.FullName
  $entry
  if($entry.PSIsContainer){Get-PlainTree $entry.FullName}
 }
}
Assert-PlainPath ([IO.Path]::GetDirectoryName($Output))
$files=New-Object 'System.Collections.Generic.List[object]'
$destinations=New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
function Add-PayloadFile([string]$Source,[string]$Destination,[string]$Pin='') {
 Assert-PlainPath $Source
 $item=Get-Item -LiteralPath $Source -Force
 if($item -isnot [IO.FileInfo] -or $item.Length -le 0){throw "Missing or empty payload file: $Source"}
 if(-not $destinations.Add($Destination)){throw "Duplicate payload path: $Destination"}
 $hash=(Get-FileHash -LiteralPath $Source -Algorithm SHA256).Hash.ToLowerInvariant()
 if($Pin -and $hash -ne $Pin){throw "Pinned dependency hash mismatch: $Destination"}
 $files.Add([pscustomobject]@{source=$item.FullName;path=$Destination;size=$item.Length;sha256=$hash})
}
function Assert-AMD64([string]$Path) {
 $stream=[IO.File]::OpenRead($Path)
 $reader=New-Object IO.BinaryReader($stream)
 try {
  if($reader.ReadUInt16() -ne 0x5a4d){throw "Not a PE image: $Path"}
  $stream.Position=0x3c
  $offset=$reader.ReadUInt32()
  if($offset -gt ($stream.Length-26)){throw "Invalid PE header: $Path"}
  $stream.Position=$offset
  if($reader.ReadUInt32() -ne 0x4550 -or $reader.ReadUInt16() -ne 0x8664){throw "Not an AMD64 PE image: $Path"}
 } finally {$reader.Dispose()}
}
function Assert-Signature([string]$Path) {
 $signature=Get-AuthenticodeSignature -LiteralPath $Path
 if($signature.Status -ne 'Valid'){throw "Authenticode validation failed: $Path ($($signature.Status))"}
}

Add-PayloadFile $Daemon 'daemon/irviod.exe'
Add-PayloadFile $Wintun 'daemon/wintun.dll' 'e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce'
Add-PayloadFile (Join-Path $WinDivertDirectory 'WinDivert.dll') 'driver/WinDivert.dll' 'c1e060ee19444a259b2162f8af0f3fe8c4428a1c6f694dce20de194ac8d7d9a2'
Add-PayloadFile (Join-Path $WinDivertDirectory 'WinDivert64.sys') 'driver/WinDivert64.sys' '8da085332782708d8767bcace5327a6ec7283c17cfb85e40b03cd2323a90ddc2'
Add-PayloadFile $GeoIP 'geo/country.mmdb'
Add-PayloadFile $GeoAttribution 'geo/ATTRIBUTION.md'

Assert-PlainPath $UIBundle
$UIBundle=(Get-Item -LiteralPath $UIBundle).FullName.TrimEnd('\')
foreach($entry in Get-PlainTree $UIBundle){
 $relative=$entry.FullName.Substring($UIBundle.Length+1).Replace('\','/')
 if($entry.PSIsContainer){
  if($relative -ne 'data' -and -not $relative.StartsWith('data/')){throw "Unexpected UI directory: $relative"}
  continue
 }
 $allowed=$relative -in @('irvio.exe','irvio-ui-bridge.exe','native_assets.json','data/icudtl.dat','data/app.so')
 $allowed=$allowed -or ($relative -match '^[A-Za-z0-9_.-]+\.dll$')
 $allowed=$allowed -or $relative.StartsWith('data/flutter_assets/')
 if(-not $allowed){throw "Unexpected UI file (do not package build/test residue): $relative"}
 if($relative -match '(?i)(^|/)(\.git|\.env|credentials|identity|profile)(/|\.|$)' -or $relative -match '(?i)\.(pdb|key|pem|ps1|bat|cmd|test\.exe)$'){throw "Forbidden UI payload: $relative"}
 Add-PayloadFile $entry.FullName ('ui/'+$relative)
}
foreach($required in @('ui/irvio.exe','ui/irvio-ui-bridge.exe','ui/flutter_windows.dll','ui/msvcp140.dll','ui/vcruntime140.dll','ui/vcruntime140_1.dll','ui/data/icudtl.dat','ui/data/app.so','ui/data/flutter_assets/NOTICES.Z')){
 if(-not $destinations.Contains($required)){throw "Missing release UI component: $required"}
}

Assert-PlainPath $Licenses
$Licenses=(Get-Item -LiteralPath $Licenses).FullName.TrimEnd('\')
foreach($entry in Get-PlainTree $Licenses){
 if($entry.PSIsContainer){continue}
 $relative=$entry.FullName.Substring($Licenses.Length+1).Replace('\','/')
 if($relative -notmatch '^[A-Za-z0-9@_./+ -]+$' -or $relative -match '(?i)\.(exe|dll|sys|key|pem|ps1|zip)$'){throw "Unexpected license artifact: $relative"}
 Add-PayloadFile $entry.FullName ('licenses/'+$relative)
}
foreach($required in @('AGPL-3.0-only.txt','WINTUN-LICENSE.txt','WINDIVERT-LICENSE.txt','GO-MODULES.txt','FLUTTER-LICENSE.txt','MSVC-LICENSE.txt')){
 if(-not $destinations.Contains('licenses/'+$required)){throw "Missing dependency notice: $required"}
}
foreach($file in $files){
 if($file.path -match '\.(exe|dll|sys)$'){Assert-AMD64 $file.source}
}
Assert-Signature $Wintun
Assert-Signature (Join-Path $WinDivertDirectory 'WinDivert64.sys')
foreach($name in @('msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll')){
 $path=Join-Path $UIBundle $name
 Assert-Signature $path
 $signature=Get-AuthenticodeSignature -LiteralPath $path
 $publisher=$signature.SignerCertificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName,$false)
 if($publisher -notin @('Microsoft Corporation','Microsoft Windows','Microsoft Windows Software Compatibility Publisher')){throw "MSVC runtime publisher mismatch: $name"}
}
if(-not $DevelopmentUnsigned){
 foreach($path in @($Daemon,(Join-Path $UIBundle 'irvio.exe'),(Join-Path $UIBundle 'irvio-ui-bridge.exe'))){Assert-Signature $path}
}

New-Item -ItemType Directory -Path $Output | Out-Null
foreach($file in $files){
 $target=Join-Path $Output $file.path
 New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($target)) | Out-Null
 Copy-Item -LiteralPath $file.source -Destination $target
 if((Get-FileHash -LiteralPath $target).Hash.ToLowerInvariant() -ne $file.sha256){throw 'Input changed while staging; discard incomplete payload'}
}
$manifest=[ordered]@{
 schema=1
 product='IRVIO'
 platform='windows-amd64'
 version=$Version
 declared_source_revision=$SourceRevision
 development_unsigned=[bool]$DevelopmentUnsigned
 created_at_utc=[DateTime]::UtcNow.ToString('o')
 files=@($files | Sort-Object path | Select-Object path,size,sha256)
}
$encoding=New-Object Text.UTF8Encoding($false)
[IO.File]::WriteAllText((Join-Path $Output 'payload-manifest.json'),($manifest|ConvertTo-Json -Depth 6),$encoding)
Write-Output "IRVIO payload staged: $Output"
