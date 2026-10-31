# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

function Assert-IRVIOPlainPath([string]$Path) {
 $item=Get-Item -LiteralPath $Path -Force -ErrorAction Stop
 while($null -ne $item){
  if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw "Reparse point: $Path"}
  if($item -is [IO.FileInfo]){$item=$item.Directory}else{$item=$item.Parent}
 }
}

function Read-IRVIOPayload([string]$Root,[string]$ManifestSHA256,[bool]$AllowDevelopment) {
 if($ManifestSHA256 -notmatch '^[a-fA-F0-9]{64}$'){throw 'Expected manifest SHA-256 required from the trusted installer'}
 Assert-IRVIOPlainPath $Root
 $Root=(Get-Item -LiteralPath $Root).FullName.TrimEnd('\')
 $path=Join-Path $Root 'payload-manifest.json'
 Assert-IRVIOPlainPath $path
 if((Get-Item -LiteralPath $path).Length -gt 4MB){throw 'Payload manifest too large'}
 if((Get-FileHash -LiteralPath $path).Hash -ne $ManifestSHA256){throw 'Payload manifest hash mismatch'}
 $manifest=Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
 if($manifest.schema -ne 1 -or $manifest.product -cne 'IRVIO' -or $manifest.platform -cne 'windows-amd64'){throw 'Unsupported payload manifest'}
 if($manifest.development_unsigned -isnot [bool]){throw 'Missing explicit payload signing mode'}
 if($manifest.development_unsigned -and -not $AllowDevelopment){throw 'Development payload is not permitted for release installation'}
 if($manifest.files.Count -lt 1 -or $manifest.files.Count -gt 20000){throw 'Invalid manifest file count'}
 $seen=New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
 foreach($file in $manifest.files){
  if($file.path -cnotmatch '^(daemon|ui|driver|geo|licenses)/[A-Za-z0-9@_./+ -]+$' -or $file.path -match '(^|/)(\.|\.\.)(/|$)' -or $file.path -match '//|[. ](/|$)'){throw 'Unsafe payload path'}
  if(-not $seen.Add($file.path)){throw 'Duplicate payload destination'}
  if($file.sha256 -notmatch '^[a-f0-9]{64}$' -or $file.size -is [string] -or $file.size -lt 0){throw 'Invalid payload file record'}
  $target=Join-Path $Root $file.path
  Assert-IRVIOPlainPath $target
  $item=Get-Item -LiteralPath $target
  if($item -isnot [IO.FileInfo] -or $item.Length -ne $file.size -or (Get-FileHash -LiteralPath $target).Hash -ne $file.sha256){throw "Payload file hash/size mismatch: $($file.path)"}
 }
 foreach($required in @('daemon/irviod.exe','daemon/wintun.dll','driver/WinDivert.dll','driver/WinDivert64.sys','geo/country.mmdb','geo/ATTRIBUTION.md','ui/irvio.exe','ui/irvio-ui-bridge.exe','ui/flutter_windows.dll','ui/msvcp140.dll','ui/vcruntime140.dll','ui/vcruntime140_1.dll','licenses/MSVC-LICENSE.txt','ui/data/icudtl.dat','ui/data/app.so','ui/data/flutter_assets/NOTICES.Z')){
  if(-not $seen.Contains($required)){throw "Missing payload component: $required"}
 }
 return $manifest
}
