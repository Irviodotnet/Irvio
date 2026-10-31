# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

. (Join-Path $PSScriptRoot 'software-update.ps1')

function Get-UninstallSCMService([string]$Name){
 if($Name -cnotin @('IRVIO','IRVIO-Recovery','WinDivert')){throw 'Unexpected uninstall service name'}
 if(-not ('IRVIOUninstall.ServiceInventory' -as [type])){
  Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
namespace IRVIOUninstall {
 public sealed class ServiceInventory {
  public uint Type, Start, State, ProcessId;
  public string Path, Account;
  [StructLayout(LayoutKind.Sequential)] struct Config {
   public uint Type, Start, Error;
   public IntPtr Path, Group;
   public uint Tag;
   public IntPtr Dependencies, Account, Display;
  }
  [StructLayout(LayoutKind.Sequential)] struct Status {
   public uint Type, State, Controls, Exit, SpecificExit, Checkpoint, Wait, ProcessId, Flags;
  }
  [DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern IntPtr OpenSCManager(string machine, string database, uint access);
  [DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern IntPtr OpenService(IntPtr manager, string name, uint access);
  [DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool QueryServiceConfig(IntPtr service, IntPtr config, uint size, out uint needed);
  [DllImport("advapi32.dll", SetLastError=true)] static extern bool QueryServiceStatusEx(IntPtr service, int level, out Status status, uint size, out uint needed);
  [DllImport("advapi32.dll")] static extern bool CloseServiceHandle(IntPtr handle);
  public static ServiceInventory Read(string name) {
   IntPtr manager=OpenSCManager(null,null,1);
   if(manager==IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
   try {
    IntPtr service=OpenService(manager,name,5);
    if(service==IntPtr.Zero) {
     int error=Marshal.GetLastWin32Error();
     if(error==1060) return null;
     throw new Win32Exception(error);
    }
    try {
     uint needed;
     if(QueryServiceConfig(service,IntPtr.Zero,0,out needed) || Marshal.GetLastWin32Error()!=122 || needed==0 || needed>65536) throw new InvalidOperationException("Invalid SCM configuration size");
     IntPtr buffer=Marshal.AllocHGlobal((int)needed);
     try {
      if(!QueryServiceConfig(service,buffer,needed,out needed)) throw new Win32Exception(Marshal.GetLastWin32Error());
      Config config=(Config)Marshal.PtrToStructure(buffer,typeof(Config));
      Status status;
      if(!QueryServiceStatusEx(service,0,out status,(uint)Marshal.SizeOf(typeof(Status)),out needed)) throw new Win32Exception(Marshal.GetLastWin32Error());
      return new ServiceInventory { Type=config.Type, Start=config.Start, Path=Marshal.PtrToStringUni(config.Path), Account=Marshal.PtrToStringUni(config.Account), State=status.State, ProcessId=status.ProcessId };
     } finally { Marshal.FreeHGlobal(buffer); }
    } finally { CloseServiceHandle(service); }
   } finally { CloseServiceHandle(manager); }
  }
 }
}
'@
 }
 return [IRVIOUninstall.ServiceInventory]::Read($Name)
}

function Read-UninstallJournal([string]$Path,[ValidateSet('desktop','server')][string]$Role='desktop'){
 Assert-IRVIOInstallerACL $Path $true
 if((Get-Item $Path).Length -gt 16KB){throw 'Oversized uninstall journal'}
 $journal=Get-Content -LiteralPath $Path -Raw|ConvertFrom-Json
 $schema=if($Role -ceq 'server'){2}else{1}
 if($journal.schema -ne $schema -or $journal.phase -cnotin @('prepared','removing','files-removed','completed') -or $journal.retain_profile -isnot [bool]){throw 'Unsupported uninstall journal'}
 if($Role -ceq 'desktop'){
  if($journal.PSObject.Properties['shortcuts']){
   $expected=@(Get-IRVIOShortcutPaths $journal.record)
   $actual=@($journal.shortcuts)
   if($actual.Count -ne $expected.Count){throw 'Uninstall shortcut choices differ'}
   for($i=0;$i -lt $expected.Count;$i++){
    if($actual[$i].path -cne $expected[$i] -or $actual[$i].sha256 -cnotmatch '^[a-f0-9]{64}$'){throw 'Invalid uninstall shortcut record'}
   }
  }elseif($journal.shortcut_sha256 -cnotmatch '^[a-f0-9]{64}$'){throw 'Unsupported uninstall shortcut record'}
 }elseif($journal.shortcut_sha256 -cne ''){throw 'Server uninstall must not own a desktop shortcut'}
 Assert-IRVIOInstallationRecord $journal.record $Role
 return $journal
}

function Get-UninstallShortcuts($Journal){
 if($Journal.PSObject.Properties['shortcuts']){return $Journal.shortcuts}
 if($Journal.record.role -ceq 'desktop'){
  [pscustomobject]@{path=(Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk');sha256=$Journal.shortcut_sha256}
 }
}

function Get-UninstallFiles($layout,$manifest,[string]$ManifestCopy){
 foreach($file in $manifest.files){
  if($layout.record.role -ceq 'server' -and $file.path.StartsWith('ui/')){continue}
  $parts=$file.path.Split('/',2)
  $base=switch($parts[0]){'daemon'{Join-Path $layout.program 'daemon'};'ui'{Join-Path $layout.program 'ui'};'driver'{$layout.driver};'geo'{Join-Path $layout.root 'geo'};'licenses'{Join-Path $layout.program 'licenses'}}
  [pscustomobject]@{path=([IO.Path]::GetFullPath((Join-Path $base $parts[1])));sha256=$file.sha256;size=$file.size}
 }
 [pscustomobject]@{path=(Join-Path $layout.program 'payload-manifest.json');sha256=$layout.record.manifest_sha256;size=(Get-Item $ManifestCopy).Length}
}

function Assert-UninstallTree([string]$Root,$Files){
 if(-not(Test-Path $Root)){return}
 Assert-IRVIOInstallerACL $Root
 $expected=New-Object 'System.Collections.Generic.Dictionary[string,object]' ([StringComparer]::OrdinalIgnoreCase)
 $directories=New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
 foreach($file in $Files){
  if(-not $file.path.StartsWith($Root+'\',[StringComparison]::OrdinalIgnoreCase)){continue}
  $expected.Add($file.path,$file)
  $parent=[IO.Path]::GetDirectoryName($file.path)
  while($parent -ine $Root){[void]$directories.Add($parent);$parent=[IO.Path]::GetDirectoryName($parent)}
 }
 $pending=New-Object 'System.Collections.Generic.Stack[string]'
 $pending.Push($Root);$count=0
 while($pending.Count){
  foreach($item in Get-ChildItem -LiteralPath $pending.Pop() -Force){
   if(++$count -gt 50000){throw 'Uninstall tree exceeds bound'}
   Assert-IRVIOInstallerACL $item.FullName
   if($item.PSIsContainer){
    if(-not $directories.Contains($item.FullName)){throw 'Unowned directory in uninstall target'}
    $pending.Push($item.FullName)
   }else{
    if(-not $expected.ContainsKey($item.FullName)){throw 'Unowned file in uninstall target'}
    $file=$expected[$item.FullName]
    if($item.Length -ne $file.size -or (Get-FileHash $item.FullName).Hash -ine $file.sha256){throw 'Modified file in uninstall target'}
   }
  }
 }
}

function Assert-UninstallService($layout,[string]$Name,[bool]$AllowMissing,[bool]$AllowRunningDriver=$false){
 $kernel=$Name -ceq 'WinDivert'
 $service=Get-UninstallSCMService $Name
 if($null -eq $service){if($AllowMissing){return};throw "Owned uninstall service missing: $Name"}
 if($kernel){
  $path=Join-Path $layout.driver 'WinDivert64.sys'
  if($service.Path -cnotin @($path,('"'+$path+'"'),('\??\'+$path)) -or $service.Type -ne 1 -or $service.Start -ne 3){throw 'Foreign uninstall driver'}
 }else{
  $path='"'+(Join-Path $layout.program 'daemon\irviod.exe')+'"'
  if($Name -ceq 'IRVIO-Recovery'){$path+=' --boot-recover'}
  elseif($layout.record.role -ceq 'server'){$path=Get-IRVIOServerCommand $layout}
  if($service.Path -cne $path -or $service.Type -ne 16 -or $service.Account -ine 'LocalSystem' -or $service.Start -ne 4 -or $service.ProcessId -ne 0){throw 'Uninstall service ownership/fence differs'}
 }
 if($service.State -ne 1 -and -not($kernel -and $AllowRunningDriver -and $service.State -eq 4)){throw "Uninstall service is not stopped: $Name"}
 return $service
}

function Assert-UninstallShortcut([string]$Path,[string]$Program){
 Assert-IRVIOInstallerACL $Path
 $shell=New-Object -ComObject WScript.Shell
 $link=$null
 try{
  $link=$shell.CreateShortcut($Path)
  if($link.TargetPath -ine (Join-Path $Program 'ui\irvio.exe') -or $link.WorkingDirectory -ine (Join-Path $Program 'ui') -or $link.Arguments){throw 'IRVIO shortcut points to another application'}
 }finally{
  if($link){[void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($link)}
  [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell)
 }
}
