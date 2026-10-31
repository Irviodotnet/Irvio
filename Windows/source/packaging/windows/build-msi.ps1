# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$WixDirectory,
 [Parameter(Mandatory=$true)][string]$Output,
 [ValidatePattern('^\d+\.\d+\.\d+$')][string]$Version='1.1.1019',
 [switch]$DevelopmentUnsigned
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if($env:OS -ne 'Windows_NT' -or ![Environment]::Is64BitProcess){throw 'Native x64 Windows build required'}
if(!$DevelopmentUnsigned){throw 'Release signing is not configured; use explicit DevelopmentUnsigned for lab packages'}
. "$PSScriptRoot\payload.ps1"
$manifest=Read-IRVIOPayload $Payload $ManifestSHA256 $true
if(Test-Path $Output){throw 'Output must be a new directory'}
$Output=[IO.Path]::GetFullPath($Output)
Assert-IRVIOPlainPath ([IO.Path]::GetDirectoryName($Output))
New-Item -ItemType Directory $Output | Out-Null
$inputs=New-Item -ItemType Directory "$Output\inputs"
Copy-Item -LiteralPath $Payload -Destination "$inputs\payload" -Recurse
$scripts=@('msi-action.ps1','install-desktop.ps1','uninstall-desktop.ps1','installation.ps1','maintenance.ps1','software-update.ps1','payload.ps1','uninstall.ps1')
foreach($name in $scripts){Copy-Item "$PSScriptRoot\$name" $inputs}
function Escape-XML([string]$s){return [Security.SecurityElement]::Escape($s)}
$components=New-Object Text.StringBuilder
$references=New-Object Text.StringBuilder
$directories=New-Object Text.StringBuilder
$index=0
function Add-Directory([string]$Path,[string]$ID) {
 [void]$directories.Append("<Directory Id=`"$ID`" Name=`"$(Escape-XML ([IO.Path]::GetFileName($Path)))`">")
 foreach($file in Get-ChildItem -LiteralPath $Path -File|Sort-Object Name){
  $script:index++
  $fileID='F'+$script:index
  $language=if([Diagnostics.FileVersionInfo]::GetVersionInfo($file.FullName).FileVersion){' DefaultLanguage="1033"'}else{''}
  [void]$components.Append("<DirectoryRef Id=`"$ID`"><Component Id=`"C$fileID`" Guid=`"*`" Win64=`"yes`"><File Id=`"$fileID`" Source=`"$(Escape-XML $file.FullName)`" KeyPath=`"yes`"$language /></Component></DirectoryRef>")
  [void]$references.Append("<ComponentRef Id=`"C$fileID`" />")
 }
 foreach($dir in Get-ChildItem -LiteralPath $Path -Directory|Sort-Object Name){
  $script:index++
  Add-Directory $dir.FullName ('D'+$script:index)
 }
 [void]$directories.Append('</Directory>')
}
Add-Directory $inputs.FullName 'CACHE'
$directoryXML=$directories.ToString().Replace('Name="inputs"','Name="IRVIO Installer"')
$actions=New-Object Text.StringBuilder
$sequence=New-Object Text.StringBuilder
foreach($entry in @(
 @('RollbackInstall','rollback','Before','InstallBackend','NOT Installed'),
 @('Install','deferred','After','InstallFiles','NOT Installed'),
 @('RollbackRemove','rollback','Before','RemoveBackend','Installed AND REMOVE="ALL"'),
 @('Remove','deferred','Before','RemoveFiles','Installed AND REMOVE="ALL"'),
 @('Commit','commit','Before','InstallFinalize','NOT Installed OR REMOVE="ALL"')
)){
 $mode=$entry[0];$id=$mode+'Backend';$cond=Escape-XML $entry[4]
 $command='"[System64Folder]WindowsPowerShell\v1.0\powershell.exe" -NoP -NonI -EP Bypass -File "[CACHE]msi-action.ps1" -A '+$mode+' -H [irvioManifestHash]'
 if($mode -ceq 'Install'){$command+=' -D "[IRVIODESKTOPSID]" -P "[INSTALLFOLDER]." -DS "[DESKTOPSHORTCUT]" -SM "[STARTMENUSHORTCUT]"'}
 if($mode -ceq 'Commit'){
  $command='"[System64Folder]WindowsPowerShell\v1.0\powershell.exe" -NoP -NonI -Command "$ErrorActionPreference = ''Stop''; Remove-Item -LiteralPath ''[CommonAppDataFolder]IRVIO\msi-transaction.json''"'
 }
 [void]$actions.Append("<CustomAction Id=`"Set$id`" Property=`"$id`" Value=`"$(Escape-XML $command)`" /><CustomAction Id=`"$id`" BinaryKey=`"WixCA`" DllEntry=`"WixQuietExec64`" Execute=`"$($entry[1])`" Impersonate=`"no`" Return=`"check`" />")
 [void]$sequence.Append("<Custom Action=`"Set$id`" Before=`"$id`">$cond</Custom><Custom Action=`"$id`" $($entry[2])=`"$($entry[3])`">$cond</Custom>")
}
$license=Get-Content (Join-Path $PSScriptRoot '..\..\LICENSE') -Raw
$rtf='{\rtf1\ansi '+$license.Replace('\','\\').Replace('{','\{').Replace('}','\}').Replace("`r",'').Replace("`n",'\par ' )+'}'
Set-Content "$Output\license.rtf" $rtf -Encoding ASCII
$xml=@"
<?xml version="1.0" encoding="utf-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
 <Product Id="*" Name="IRVIO (Preview)" Language="1033" Version="$Version" Manufacturer="IRVIO Project" UpgradeCode="677B6D80-1DC9-4496-887E-B7EBC71B277D">
  <Package InstallerVersion="500" Compressed="yes" InstallScope="perMachine" Platform="x64" />
  <MediaTemplate EmbedCab="yes" />
  <Upgrade Id="677B6D80-1DC9-4496-887E-B7EBC71B277D"><UpgradeVersion Minimum="0.0.0" IncludeMinimum="yes" OnlyDetect="yes" Property="OTHERIRVIO" /></Upgrade>
  <Property Id="IRVIODESKTOPSID" Secure="yes" />
  <Property Id="irvioManifestHash" Value="$ManifestSHA256" />
  <Property Id="INSTALLFOLDER" Secure="yes" />
  <Property Id="WIXUI_INSTALLDIR" Value="INSTALLFOLDER" />
  <Property Id="DESKTOPSHORTCUT" Value="1" Secure="yes" />
  <Property Id="STARTMENUSHORTCUT" Value="1" Secure="yes" />
  <Property Id="WINDOWSBUILDREAL"><RegistrySearch Id="WindowsBuild" Root="HKLM" Key="SOFTWARE\Microsoft\Windows NT\CurrentVersion" Name="CurrentBuildNumber" Type="raw" Win64="yes" /></Property>
  <Property Id="ARPNOREPAIR" Value="1" />
  <Property Id="ARPURLINFOABOUT" Value="https://irvio.net" />
  <Condition Message="Windows 11 x64 is required.">Installed OR (VersionNT64 AND WINDOWSBUILDREAL &gt;= 22000)</Condition>
  <Condition Message="Uninstall the previous MSI first. Profiles are retained.">Installed OR NOT OTHERIRVIO</Condition>
  <Condition Message="Repair is not supported in this preview. Uninstall and reinstall to repair.">NOT REINSTALL</Condition>
  <Condition Message="MSI rollback must be enabled.">NOT RollbackDisabled</Condition>
  <Directory Id="TARGETDIR" Name="SourceDir"><Directory Id="ProgramFiles64Folder">$directoryXML<Directory Id="INSTALLFOLDER" Name="IRVIO" /></Directory></Directory>
  $components
  <Feature Id="Desktop" Title="IRVIO Desktop" Level="1">$references</Feature>
  <CustomAction Id="DefaultDesktopSID" Property="IRVIODESKTOPSID" Value="[UserSID]" />
  $actions
  <InstallExecuteSequence><Custom Action="DefaultDesktopSID" Before="InstallInitialize">NOT Installed AND NOT IRVIODESKTOPSID</Custom>$sequence</InstallExecuteSequence>
  <UIRef Id="IRVIO_InstallUI" /><WixVariable Id="WixUILicenseRtf" Value="$(Escape-XML "$Output\license.rtf")" />
 </Product>
</Wix>
"@
Set-Content "$Output\irvio.wxs" $xml -Encoding UTF8
& "$WixDirectory\candle.exe" -nologo -arch x64 -out "$Output\irvio.wixobj" "$Output\irvio.wxs"
if($LASTEXITCODE){throw 'WiX compilation failed'}
& "$WixDirectory\candle.exe" -nologo -arch x64 -out "$Output\ui.wixobj" "$PSScriptRoot\msi-ui.wxs"
if($LASTEXITCODE){throw 'WiX UI compilation failed'}
& "$WixDirectory\light.exe" -nologo -ext WixUtilExtension -ext WixUIExtension -out "$Output\irvio-$Version-preview-x64.msi" "$Output\irvio.wixobj" "$Output\ui.wixobj"
if($LASTEXITCODE){throw 'WiX linking/validation failed'}
Get-FileHash "$Output\irvio-$Version-preview-x64.msi" | Format-List
