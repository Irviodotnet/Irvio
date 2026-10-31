# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

. (Join-Path $PSScriptRoot 'maintenance.ps1')

function Open-UpdateLock([string]$Root){
 Assert-IRVIOInstallerACL $Root $true
 $path=Join-Path $Root 'software-update.lock'
 if(Test-Path -LiteralPath $path){Assert-IRVIOInstallerACL $path}
 $file=[IO.File]::Open($path,[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
 try{Assert-IRVIOInstallerACL $path}catch{$file.Dispose();throw}
 return $file
}

function New-UpdateDirectory([string]$Path,[bool]$Readable=$false){
 if(Test-Path -LiteralPath $Path){throw "Update path already exists: $Path"}
 Assert-IRVIOInstallerACL ([IO.Path]::GetDirectoryName($Path))
 New-Item -ItemType Directory -Path $Path|Out-Null
 $acl=New-Object Security.AccessControl.DirectorySecurity
 $sddl='O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)'
 if($Readable){$sddl+='(A;OICI;0x1200a9;;;BU)'}
 $acl.SetSecurityDescriptorSddlForm($sddl)
 Set-Acl -LiteralPath $Path $acl
}

function Protect-UpdateFile([string]$Path){
 $acl=New-Object Security.AccessControl.FileSecurity
 $acl.SetSecurityDescriptorSddlForm('O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)')
 Set-Acl -LiteralPath $Path $acl
}

function Write-UpdateJSON([string]$Path,$Value){
 $temporary=$Path+'.new'
 $bytes=[Text.Encoding]::UTF8.GetBytes(($Value|ConvertTo-Json -Depth 8))
 $file=[IO.File]::Open($temporary,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
 try{$file.Write($bytes,0,$bytes.Length);$file.Flush($true)}finally{$file.Dispose()}
 Protect-UpdateFile $temporary
 if(Test-Path -LiteralPath $Path){[IO.File]::Replace($temporary,$Path,[NullString]::Value)}else{[IO.File]::Move($temporary,$Path)}
}

function Assert-OwnedTree([string]$Directory,[string[]]$Expected){
 $set=New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
 foreach($relative in $Expected){[void]$set.Add($relative.Replace('/','\'))}
 $pending=New-Object 'System.Collections.Generic.Stack[string]'
 $pending.Push($Directory);$count=0
 while($pending.Count){
  foreach($item in Get-ChildItem -LiteralPath $pending.Pop() -Force){
   if(++$count -gt 50000){throw 'Installed directory inventory exceeded bound'}
   Assert-IRVIOPlainPath $item.FullName
   if($item.PSIsContainer){$pending.Push($item.FullName)}
   elseif(-not $set.Remove($item.FullName.Substring($Directory.Length+1))){throw "Unowned file in update target: $($item.FullName)"}
  }
 }
 if($set.Count){throw 'Owned update files are missing'}
}

function Assert-UpdateImageOwned($Image,[string]$Geo){
 $components=if($Image.record.role -ceq 'server'){'^(daemon|licenses)/'}else{'^(daemon|ui|licenses)/'}
 $programFiles=@('payload-manifest.json')+@($Image.manifest.files|Where-Object {$_.path -match $components}|ForEach-Object path)
 $geoFiles=@($Image.manifest.files|Where-Object {$_.path.StartsWith('geo/')}|ForEach-Object {$_.path.Substring(4)})
 Assert-OwnedTree $Image.program $programFiles
 Assert-OwnedTree $Geo $geoFiles
}

function New-UpdateInstallationRecord($Previous,[string]$ManifestSHA256,[bool]$DevelopmentUnsigned,[ValidateSet('desktop','server')][string]$Role){
 Assert-IRVIOInstallationRecord $Previous $Role
 $record=$Previous|ConvertTo-Json -Depth 4|ConvertFrom-Json
 $record.manifest_sha256=$ManifestSHA256.ToLowerInvariant()
 $record.development_unsigned=$DevelopmentUnsigned
 Assert-IRVIOInstallationRecord $record $Role
 return $record
}
