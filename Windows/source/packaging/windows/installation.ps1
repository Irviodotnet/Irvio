# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

. (Join-Path $PSScriptRoot 'payload.ps1')

function Resolve-IRVIOProgramDirectory([string]$Path) {
 if(!$Path){$Path=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO'}
 if($Path -notmatch '^[A-Za-z]:\\' -or $Path.Substring(3) -match '[<>:"|?*\x00-\x1f]' -or $Path -match '(^|\\)\.\.(\\|$)'){throw 'Choose an absolute folder on a local fixed drive'}
 $full=[IO.Path]::GetFullPath($Path).TrimEnd('\')
 if($full.Length -le 3 -or $full -match '[. ](\\|$)'){throw 'Choose a dedicated IRVIO application folder'}
 $drive=New-Object IO.DriveInfo ([IO.Path]::GetPathRoot($full))
 if($drive.DriveType -ne [IO.DriveType]::Fixed -or $drive.DriveFormat -ne 'NTFS'){throw 'IRVIO requires a local fixed NTFS drive'}
 foreach($reserved in @([Environment]::GetFolderPath('Windows'),[Environment]::GetFolderPath('CommonApplicationData'),(Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Exit Driver'),(Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Installer'))){
  if($full -ieq $reserved -or $full.StartsWith($reserved+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'Choose an application folder outside Windows, ProgramData and IRVIO system directories'}
 }
 return $full
}

function Get-IRVIOProgramDirectory($Record) {
 if($Record.PSObject.Properties['program_directory']){return Resolve-IRVIOProgramDirectory $Record.program_directory}
 return Resolve-IRVIOProgramDirectory ''
}

function Assert-IRVIOInstallParent([string]$Path) {
 Assert-IRVIOPlainPath $Path
 $item=Get-Item -LiteralPath $Path
 $trusted=@('S-1-5-18','S-1-5-32-544','S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464')
 $replace=[Security.AccessControl.FileSystemRights]::Delete -bor [Security.AccessControl.FileSystemRights]::DeleteSubdirectoriesAndFiles -bor [Security.AccessControl.FileSystemRights]::ChangePermissions -bor [Security.AccessControl.FileSystemRights]::TakeOwnership
 while($item){
  $acl=Get-Acl -LiteralPath $item.FullName
  if($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin $trusted){throw 'Installation parent must be owned by Administrators or the system, not a user profile'}
  foreach($ace in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])){
   if(($ace.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) -ne 0){continue}
   if($ace.AccessControlType -eq 'Allow' -and ($ace.FileSystemRights -band $replace) -and $ace.IdentityReference.Value -notin $trusted){throw 'Installation parent permits unprivileged replacement; choose a protected folder'}
  }
  $item=$item.Parent
 }
}

function Get-IRVIOShortcutPaths($Record) {
 $start=$true;$desktop=$false
 if($Record.PSObject.Properties['start_menu_shortcut']){$start=$Record.start_menu_shortcut}
 if($Record.PSObject.Properties['desktop_shortcut']){$desktop=$Record.desktop_shortcut}
 if($start){Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk'}
 if($desktop){Join-Path ([Environment]::GetFolderPath('CommonDesktopDirectory')) 'IRVIO.lnk'}
}

function Read-IRVIODeploymentRecord([string]$Root) {
 foreach($name in @('installation.json','uninstall.json','uninstalled.json')){
  $path=Join-Path $Root $name
  if(Test-Path -LiteralPath $path){
   Assert-IRVIOInstallerACL $path $true
   if((Get-Item -LiteralPath $path).Length -gt 16KB){throw 'Oversized installation metadata'}
   $value=Get-Content -LiteralPath $path -Raw|ConvertFrom-Json
   if($name -ne 'installation.json'){$value=$value.record}
   Assert-IRVIOInstallationRecord $value
   return $value
  }
 }
 throw 'Installation ownership record is missing'
}

function Assert-IRVIOInstallerACL([string]$Path,[bool]$RequireProtected=$false) {
 Assert-IRVIOPlainPath $Path
 $acl=Get-Acl -LiteralPath $Path -ErrorAction Stop
 $owner=$acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
 if($owner -notin @('S-1-5-18','S-1-5-32-544')){throw "Installation path has an untrusted owner: $Path"}
 if($RequireProtected -and -not $acl.AreAccessRulesProtected){throw "Installation root ACL is inherited: $Path"}
 $writes=[Security.AccessControl.FileSystemRights]::Write -bor [Security.AccessControl.FileSystemRights]::Delete -bor [Security.AccessControl.FileSystemRights]::DeleteSubdirectoriesAndFiles -bor [Security.AccessControl.FileSystemRights]::ChangePermissions -bor [Security.AccessControl.FileSystemRights]::TakeOwnership
 foreach($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])){
  if($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and ($rule.FileSystemRights -band $writes) -and $rule.IdentityReference.Value -notin @('S-1-5-18','S-1-5-32-544')){throw "Installation path is writable by another principal: $Path"}
 }
}

function Read-IRVIOInstalledFiles([string]$Root,[string]$Program,[string]$Driver) {
 $recordPath=Join-Path $Root 'installation.json'
 Assert-IRVIOInstallerACL $recordPath $true
 if((Get-Item -LiteralPath $recordPath).Length -gt 16KB){throw 'Installation metadata is oversized'}
 $record=Get-Content -LiteralPath $recordPath -Raw | ConvertFrom-Json
 return Read-IRVIOInstalledImage $Root $Program $Driver $record (Join-Path $Root 'geo')
}

function Assert-IRVIOInstallationRecord($record,[ValidateSet('desktop','server')][string]$Role='desktop') {
 if($record.schema -ne 1 -or $record.role -cne $Role -or $record.manifest_sha256 -notmatch '^[a-f0-9]{64}$' -or $record.development_unsigned -isnot [bool]){throw 'Unsupported installation record'}
 if($Role -ceq 'desktop'){
  if($record.desktop_sid -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$'){throw 'Unsupported installation record'}
  if($record.PSObject.Properties['program_directory']){
   if($record.program_directory -isnot [string] -or (Resolve-IRVIOProgramDirectory $record.program_directory) -cne $record.program_directory){throw 'Invalid recorded installation directory'}
   foreach($name in @('desktop_shortcut','start_menu_shortcut')){if($record.$name -isnot [bool]){throw 'Invalid shortcut preference'}}
  }
 }elseif($record.bootstrap_sha256 -cnotmatch '^[a-f0-9]{64}$'){throw 'Unsupported server bootstrap record'}
}

function Read-IRVIOPreservedInstallation([string]$Root,[string]$DesktopSID){
 Assert-IRVIOInstallerACL $Root $true
 $path=Join-Path $Root 'uninstalled.json'
 Assert-IRVIOInstallerACL $path $true
 if((Get-Item $path).Length -gt 16KB){throw 'Oversized retained installation receipt'}
 $receipt=Get-Content -LiteralPath $path -Raw|ConvertFrom-Json
 if($receipt.schema -ne 1 -or $receipt.phase -cne 'completed' -or $receipt.retain_profile -isnot [bool]){throw 'Unsupported retained installation receipt'}
 Assert-IRVIOInstallationRecord $receipt.record
 $sid=Join-Path $Root 'desktop-user.sid'
 Assert-IRVIOInstallerACL $sid $true
 if($receipt.record.desktop_sid -cne $DesktopSID -or [IO.File]::ReadAllText($sid).Trim() -cne $DesktopSID){throw 'Retained profile belongs to another desktop user'}
 if(-not $receipt.retain_profile){Assert-IRVIOErasedProfile $Root}
 return $receipt
}

function Assert-IRVIOErasedProfile([string]$Root){
 $profile=Join-Path $Root 'profile'
 if(Test-Path -LiteralPath $profile){
  Assert-IRVIOInstallerACL $profile $true
  if(@(Get-ChildItem -LiteralPath $profile -Force).Count){throw 'Profile data appeared after explicit erasure'}
 }
}

function Read-IRVIOInstalledManifest([string]$manifestPath,$record,[ValidateSet('desktop','server')][string]$Role='desktop') {
 Assert-IRVIOInstallationRecord $record $Role
 Assert-IRVIOInstallerACL $manifestPath
 if((Get-Item -LiteralPath $manifestPath).Length -gt 4MB){throw 'Installation metadata is oversized'}
 if((Get-FileHash -LiteralPath $manifestPath).Hash -ne $record.manifest_sha256){throw 'Installed manifest differs from protected installation record'}
 $manifest=Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
 if($manifest.schema -ne 1 -or $manifest.product -cne 'IRVIO' -or $manifest.platform -cne 'windows-amd64' -or $manifest.development_unsigned -isnot [bool] -or $manifest.development_unsigned -ne $record.development_unsigned){throw 'Unsupported installed manifest'}
 if($manifest.files.Count -lt 1 -or $manifest.files.Count -gt 20000){throw 'Invalid installed file count'}
 $seen=New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
 foreach($file in $manifest.files){
  if($file.path -cnotmatch '^(daemon|ui|driver|geo|licenses)/[A-Za-z0-9@_./+ -]+$' -or $file.path -match '(^|/)(\.|\.\.)(/|$)|//|[. ](/|$)' -or -not $seen.Add($file.path)){throw 'Unsafe or duplicate installed payload path'}
  if($file.sha256 -notmatch '^[a-f0-9]{64}$' -or $file.size -is [string] -or $file.size -lt 0){throw 'Invalid installed file record'}
 }
 foreach($required in @('daemon/irviod.exe','daemon/wintun.dll','driver/WinDivert.dll','driver/WinDivert64.sys','ui/irvio.exe','ui/irvio-ui-bridge.exe','geo/country.mmdb')){
  if(-not $seen.Contains($required)){throw "Incomplete installed payload: $required"}
 }
 return $manifest
}

function Read-IRVIOInstalledImage([string]$Root,[string]$Program,[string]$Driver,$record,[string]$Geo,[ValidateSet('desktop','server')][string]$Role='desktop') {
 foreach($base in @($Root,$Program,$Driver)){Assert-IRVIOInstallerACL $base $true}
 Assert-IRVIOInstallerACL $Geo
 $manifest=Read-IRVIOInstalledManifest (Join-Path $Program 'payload-manifest.json') $record $Role
 $parents=New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
 $files=New-Object 'System.Collections.Generic.List[object]'
 foreach($file in $manifest.files){
  $parts=$file.path.Split('/',2)
  if($Role -ceq 'server' -and $parts[0] -ceq 'ui'){continue}
  $base=switch($parts[0]){'daemon'{"$Program\daemon"};'ui'{"$Program\ui"};'driver'{$Driver};'geo'{$Geo};'licenses'{"$Program\licenses"}}
  $target=Join-Path $base $parts[1]
  Assert-IRVIOInstallerACL $target ($Role -ceq 'server' -and $parts[0] -in @('daemon','driver','geo'))
  $parent=[IO.Path]::GetDirectoryName($target)
  while($parent -and $parent -notin @($Root,$Program,$Driver,$Geo)){
   if($parents.Add($parent)){Assert-IRVIOInstallerACL $parent}
   $parent=[IO.Path]::GetDirectoryName($parent)
  }
  $item=Get-Item -LiteralPath $target
  if($item -isnot [IO.FileInfo] -or $item.Length -ne $file.size -or (Get-FileHash -LiteralPath $target).Hash -ne $file.sha256){throw "Installed file changed: $($file.path)"}
  $files.Add([pscustomobject]@{path=$target;component=$parts[0];sha256=$file.sha256;size=$file.size})
 }
 if($Role -ceq 'desktop'){
  $sidPath=Join-Path $Root 'desktop-user.sid'
  Assert-IRVIOInstallerACL $sidPath $true
  if([IO.File]::ReadAllText($sidPath).Trim() -cne $record.desktop_sid){throw 'Desktop authorization differs from installation record'}
 }elseif((Test-Path (Join-Path $Root 'desktop-user.sid')) -or (Test-Path (Join-Path $Program 'ui'))){throw 'Server installation contains desktop authorization or UI'}
 return [pscustomobject]@{record=$record;manifest=$manifest;files=@($files.ToArray());root=$Root;program=$Program;driver=$Driver}
}

function Assert-IRVIOInstalledServices($Installation,$Service,$Recovery,$Kernel) {
 $command='"'+$Installation.program+'\daemon\irviod.exe"'
 if($null -eq $Service -or $Service.Name -cne 'IRVIO' -or $Service.PathName -cne $command -or $Service.StartName -ine 'LocalSystem' -or $Service.ServiceType -cne 'Own Process'){throw 'IRVIO service does not match the owned installation'}
 if($null -eq $Recovery -or $Recovery.Name -cne 'IRVIO-Recovery' -or $Recovery.PathName -cne "$command --boot-recover" -or $Recovery.StartName -ine 'LocalSystem' -or $Recovery.ServiceType -cne 'Own Process'){throw 'Recovery service does not match the owned installation'}
 $driverPath=Join-Path $Installation.driver 'WinDivert64.sys'
 if($null -eq $Kernel -or $Kernel.Name -cne 'WinDivert' -or $Kernel.ServiceType -cne 'Kernel Driver' -or $Kernel.StartMode -cne 'Manual' -or $Kernel.PathName -cnotin @($driverPath,('"'+$driverPath+'"'),('\??\'+$driverPath))){throw 'WinDivert service does not match the owned installation'}
}

function Read-IRVIOInstallation {
 $principal=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
 if(-not [Environment]::Is64BitProcess -or -not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Elevated 64-bit Windows installer required'}
 $root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO'
 $program=Get-IRVIOProgramDirectory (Read-IRVIODeploymentRecord $root)
 $driver=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Exit Driver'
 $installed=Read-IRVIOInstalledFiles $root $program $driver
 $service=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
 $recovery=Get-CimInstance Win32_Service -Filter "Name='IRVIO-Recovery'"
 $kernel=Get-CimInstance Win32_SystemDriver -Filter "Name='WinDivert'"
 Assert-IRVIOInstalledServices $installed $service $recovery $kernel
 return $installed
}
