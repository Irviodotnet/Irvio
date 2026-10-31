# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$DesktopSID,
 [string]$InstallDirectory='',
 [bool]$DesktopShortcut=$false,
 [bool]$StartMenuShortcut=$true,
 [switch]$DevelopmentUnsigned
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'installation.ps1')
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
$principal=New-Object Security.Principal.WindowsPrincipal($identity)
if(-not [Environment]::Is64BitProcess -or -not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Elevated 64-bit Windows installer required'}
if([Environment]::OSVersion.Version.Build -lt 22000){throw 'Windows 11 or newer is required'}
if($DesktopSID -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$'){throw 'Explicit desktop user SID required'}
$user=@(Get-LocalUser | Where-Object {$_.SID.Value -eq $DesktopSID -and $_.Enabled})
if($user.Count -ne 1){throw 'Desktop SID must identify one enabled local user'}
$manifest=Read-IRVIOPayload $Payload $ManifestSHA256 ([bool]$DevelopmentUnsigned)
$Payload=(Get-Item -LiteralPath $Payload).FullName
$root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO'
$program=Resolve-IRVIOProgramDirectory $InstallDirectory
$driver=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Exit Driver'
$shortcuts=@(Get-IRVIOShortcutPaths ([pscustomobject]@{desktop_shortcut=$DesktopShortcut;start_menu_shortcut=$StartMenuShortcut}))
$preserved=$null
if(Test-Path $root){
 if(-not(Test-Path (Join-Path $root 'uninstalled.json'))){throw "Existing installation/object; use a supported upgrade, not overwrite: $root"}
 $preserved=Read-IRVIOPreservedInstallation $root $DesktopSID
 foreach($name in @('installation.json','uninstall.json','maintenance.json','software-update','geo')){
  if(Test-Path (Join-Path $root $name)){throw 'Retained data has unfinished maintenance or installation objects'}
 }
}
foreach($path in (@($root,$driver)+$shortcuts)){
 if($path -ceq $root -and $null -ne $preserved){continue}
 Assert-IRVIOPlainPath ([IO.Path]::GetDirectoryName($path))
 if(Test-Path -LiteralPath $path){throw "Existing installation/object; use a supported upgrade, not overwrite: $path"}
}
if(Test-Path -LiteralPath $program){throw "Application folder already exists: $program"}
$parent=[IO.Path]::GetDirectoryName($program)
$missingParents=New-Object 'System.Collections.Generic.List[string]'
while(!(Test-Path -LiteralPath $parent)){$missingParents.Add($parent);$parent=[IO.Path]::GetDirectoryName($parent)}
Assert-IRVIOInstallParent $parent
foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){
 if(Get-Service -Name $name -ErrorAction SilentlyContinue){throw "Existing service: $name"}
}
$createdDirectories=New-Object 'System.Collections.Generic.List[string]'
$createdServices=New-Object 'System.Collections.Generic.List[string]'
$createdShortcuts=New-Object 'System.Collections.Generic.List[string]'
$createdInstallationRecord=$false
if(-not ('IRVIOInstaller.NativeDirectory' -as [type])){
 Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
namespace IRVIOInstaller {
 public static class NativeDirectory {
  [StructLayout(LayoutKind.Sequential)] struct Attributes { public int Length; public IntPtr Descriptor; public int Inherit; }
  [DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool ConvertStringSecurityDescriptorToSecurityDescriptor(string text, uint revision, out IntPtr descriptor, out uint size);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool CreateDirectory(string path, ref Attributes attributes);
  [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr memory);
  public static void Create(string path, string sddl) {
   IntPtr descriptor; uint size;
   if(!ConvertStringSecurityDescriptorToSecurityDescriptor(sddl, 1, out descriptor, out size)) throw new Win32Exception(Marshal.GetLastWin32Error());
   try {
    var attributes = new Attributes { Length=Marshal.SizeOf(typeof(Attributes)), Descriptor=descriptor, Inherit=0 };
    if(!CreateDirectory(path, ref attributes)) throw new Win32Exception(Marshal.GetLastWin32Error());
   } finally { LocalFree(descriptor); }
  }
 }
}
'@
}
function Protect-Directory([string]$Path,[bool]$Readable) {
 $descriptor='O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)'
 if($Readable){$descriptor+='(A;OICI;0x1200a9;;;BU)'}
 [IRVIOInstaller.NativeDirectory]::Create($Path,$descriptor)
 $createdDirectories.Add($Path)
}
function Protect-File([string]$Path,[bool]$Readable=$false) {
 $acl=New-Object Security.AccessControl.FileSecurity
 $descriptor='O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)'
 if($Readable){$descriptor+='(A;;0x1200a9;;;BU)'}
 $acl.SetSecurityDescriptorSddlForm($descriptor)
 Set-Acl -LiteralPath $Path -AclObject $acl
}
function Invoke-IRVIOServiceControl([string[]]$Arguments) {
 & "$env:SystemRoot\System32\sc.exe" @Arguments | Out-Null
 if($LASTEXITCODE){throw "SCM configuration failed ($($Arguments[0])): $LASTEXITCODE"}
}
try {
 for($i=$missingParents.Count-1;$i -ge 0;$i--){Protect-Directory $missingParents[$i] $true}
 Protect-Directory $program $true
 Protect-Directory "$program\daemon" $false
 if($null -eq $preserved){Protect-Directory $root $false}
 Protect-Directory "$root\geo" $false
 Protect-Directory $driver $false
 foreach($file in $manifest.files){
  $parts=$file.path.Split('/',2)
  $base=switch($parts[0]){'daemon'{"$program\daemon"};'ui'{"$program\ui"};'driver'{$driver};'geo'{"$root\geo"};'licenses'{"$program\licenses"};default{throw 'Unsupported payload component'}}
  $target=Join-Path $base $parts[1]
  New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($target)) | Out-Null
  Copy-Item -LiteralPath (Join-Path $Payload $file.path) -Destination $target
  if((Get-FileHash -LiteralPath $target).Hash -ne $file.sha256){throw 'Payload changed during installation'}
  if($parts[0] -in @('daemon','driver','geo')){Protect-File $target}
 }
 if($null -eq $preserved){
  [IO.File]::WriteAllText("$root\desktop-user.sid",$DesktopSID,[Text.Encoding]::ASCII)
  Protect-File "$root\desktop-user.sid"
 }
 $command='"'+$program+'\daemon\irviod.exe"'
 Invoke-IRVIOServiceControl @('create','WinDivert','type=','kernel','start=','demand','binPath=',('"'+$driver+'\WinDivert64.sys"'))
 $createdServices.Add('WinDivert')
 New-Service -Name IRVIO-Recovery -BinaryPathName "$command --boot-recover" -StartupType Automatic -DisplayName 'IRVIO boot recovery' | Out-Null
 $createdServices.Add('IRVIO-Recovery')
 New-Service -Name IRVIO -BinaryPathName $command -StartupType Disabled -DisplayName 'IRVIO' -DependsOn WinDivert | Out-Null
 $createdServices.Add('IRVIO')
 Invoke-IRVIOServiceControl @('failure','IRVIO','reset=','86400','actions=','restart/1000/restart/3000')
 Invoke-IRVIOServiceControl @('failureflag','IRVIO','1')
 Invoke-IRVIOServiceControl @('sdset','IRVIO',('D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;CCLCRP;;;'+$DesktopSID+')'))
 $shell=New-Object -ComObject WScript.Shell
 try {
 foreach($shortcut in $shortcuts){
  $link=$shell.CreateShortcut($shortcut)
  $link.TargetPath="$program\ui\irvio.exe"
  $link.WorkingDirectory="$program\ui"
  $link.Description='IRVIO'
  $createdShortcuts.Add($shortcut)
  $link.Save()
  Protect-File $shortcut $true
 }
 } finally {if($null -ne $shell){[void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell)}}
 Copy-Item -LiteralPath (Join-Path $Payload 'payload-manifest.json') -Destination "$program\payload-manifest.json"
 if((Get-FileHash -LiteralPath "$program\payload-manifest.json").Hash -ne $ManifestSHA256){throw 'Payload manifest changed during installation'}
 $record=@{schema=1;manifest_sha256=$ManifestSHA256.ToLowerInvariant();desktop_sid=$DesktopSID;role='desktop';development_unsigned=[bool]$manifest.development_unsigned;program_directory=$program;desktop_shortcut=$DesktopShortcut;start_menu_shortcut=$StartMenuShortcut}
 $createdInstallationRecord=$true
 [IO.File]::WriteAllText("$root\installation.json",($record|ConvertTo-Json),[Text.Encoding]::UTF8)
 Protect-File "$root\installation.json"
 Invoke-IRVIOServiceControl @('config','IRVIO','start=','demand')
 if($null -ne $preserved){
  Read-IRVIOPreservedInstallation $root $DesktopSID|Out-Null
  $oldManifest=Join-Path $root 'uninstall-manifest.json'
  if(Test-Path $oldManifest){
   Read-IRVIOInstalledManifest $oldManifest $preserved.record|Out-Null
   Remove-Item -LiteralPath $oldManifest
  }
  Remove-Item -LiteralPath (Join-Path $root 'uninstalled.json')
 }
} catch {
 $failure=$_
 foreach($name in $createdServices){
  $service=Get-Service $name
  if($service.Status -ne 'Stopped'){throw "Installation failed and service $name started externally; retain installation for safe teardown: $failure"}
 }
 for($i=$createdServices.Count-1;$i -ge 0;$i--){Invoke-IRVIOServiceControl @('delete',$createdServices[$i])}
 foreach($shortcut in $createdShortcuts){if(Test-Path -LiteralPath $shortcut){Remove-Item -LiteralPath $shortcut}}
 if($null -ne $preserved -and $createdInstallationRecord -and (Test-Path "$root\installation.json")){
  Assert-IRVIOInstallerACL "$root\installation.json"
  Remove-Item -LiteralPath "$root\installation.json"
 }
 for($i=$createdDirectories.Count-1;$i -ge 0;$i--){
  $path=$createdDirectories[$i]
  if(Test-Path -LiteralPath $path){Assert-IRVIOPlainPath $path;Remove-Item -LiteralPath $path -Recurse}
 }
 throw $failure
}
'IRVIO desktop installed; service is demand-start and no VPN connection was initiated.'
