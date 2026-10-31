# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$Go,
 [Parameter(Mandatory=$true)][string]$Windres,
 [Parameter(Mandatory=$true)][string]$Output,
 [switch]$DevelopmentUnsigned
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
if($env:OS -ne 'Windows_NT' -or -not [Environment]::Is64BitProcess){throw 'Native 64-bit Windows build required'}
if(-not $DevelopmentUnsigned){throw 'Release setup signing/provenance gate is not configured; only explicit development builds are enabled'}
. (Join-Path $PSScriptRoot 'payload.ps1')
. (Join-Path $PSScriptRoot 'setup-manifest.ps1')
$manifest=Read-IRVIOPayload $Payload $ManifestSHA256 $true
$Payload=(Get-Item -LiteralPath $Payload).FullName
$Output=[IO.Path]::GetFullPath($Output)
if(Test-Path -LiteralPath $Output){throw 'New build output required'}
Assert-IRVIOPlainPath ([IO.Path]::GetDirectoryName($Output))
Assert-IRVIOPlainPath $Go
Assert-IRVIOPlainPath $Windres
$repo=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$archive=Join-Path $repo 'cmd\irvio-setup\bundle\payload.zip'
$resource=Join-Path $repo 'cmd\irvio-setup\setup_windows_amd64.syso'
Assert-IRVIOPlainPath ([IO.Path]::GetDirectoryName($archive))
if(Test-Path -LiteralPath $archive){throw 'Installer bundle input already exists; do not overwrite an active build'}
if(Test-Path -LiteralPath $resource){throw 'Installer manifest resource already exists; do not overwrite an active build'}
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$stream=[IO.File]::Open($archive,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
$zip=$null
try {
 $zip=New-Object IO.Compression.ZipArchive($stream,[IO.Compression.ZipArchiveMode]::Create,$true)
 foreach($file in $manifest.files){
  $source=Join-Path $Payload $file.path
  [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,$source,('payload/'+$file.path),[IO.Compression.CompressionLevel]::Optimal)|Out-Null
 }
 [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,(Join-Path $Payload 'payload-manifest.json'),'payload/payload-manifest.json',[IO.Compression.CompressionLevel]::Optimal)|Out-Null
 $scripts=@('install-or-update.ps1','install-or-update-server.ps1','install-desktop.ps1','install-server.ps1','server-installation.ps1','update-desktop.ps1','recover-update.ps1','software-update.ps1','maintenance.ps1','installation.ps1','payload.ps1','uninstall-for-user.ps1','uninstall-desktop.ps1','uninstall.ps1')
 foreach($name in $scripts){
  [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,(Join-Path $PSScriptRoot $name),('scripts/'+$name),[IO.Compression.CompressionLevel]::Optimal)|Out-Null
 }
 $zip.Dispose();$zip=$null
 $stream.Dispose();$stream=$null
 Read-IRVIOPayload $Payload $ManifestSHA256 $true|Out-Null
 New-Item -ItemType Directory $Output|Out-Null
 Push-Location (Join-Path $repo 'cmd\irvio-setup')
 try {
  & $Windres -i setup.rc -o $resource -O coff
  if($LASTEXITCODE -or -not(Test-Path -LiteralPath $resource)){throw 'Installer asInvoker manifest resource build failed'}
 }finally{Pop-Location}
 $oldOS=$env:GOOS;$oldArch=$env:GOARCH;$oldCGO=$env:CGO_ENABLED
 Push-Location $repo
 try {
  $env:GOOS='windows';$env:GOARCH='amd64';$env:CGO_ENABLED='0'
  & $Go build -trimpath -ldflags "-H windowsgui -X main.manifestSHA=$($ManifestSHA256.ToLowerInvariant()) -X main.development=true" -o "$Output\irvio-setup-development.exe" ./cmd/irvio-setup
  if($LASTEXITCODE){throw 'Installer build failed'}
 }finally{Pop-Location;$env:GOOS=$oldOS;$env:GOARCH=$oldArch;$env:CGO_ENABLED=$oldCGO}
 Read-IRVIOSetupManifest "$Output\irvio-setup-development.exe"|Set-Content "$Output\setup-manifest.xml"
 & "$Output\irvio-setup-development.exe" --verify-bundle > "$Output\bundle-check.txt"
 if($LASTEXITCODE){throw 'Built installer rejected its own embedded payload'}
 $goVersion=& $Go version
 $backends=@{}
 foreach($name in $scripts){$backends[$name]=(Get-FileHash (Join-Path $PSScriptRoot $name)).Hash.ToLowerInvariant()}
 @{development_only=$true;manifest_sha256=$ManifestSHA256.ToLowerInvariant();payload_version=$manifest.version;go=$goVersion;sha256=(Get-FileHash "$Output\irvio-setup-development.exe").Hash.ToLowerInvariant();backends=$backends}|ConvertTo-Json|Set-Content "$Output\build.json"
}finally{
 if($zip){$zip.Dispose()}
 if($stream){$stream.Dispose()}
 if(Test-Path -LiteralPath $archive){Remove-Item -LiteralPath $archive}
 if(Test-Path -LiteralPath $resource){Remove-Item -LiteralPath $resource}
}
'Development-only single EXE built; no install, upgrade, uninstall or release acceptance claimed.'
