# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

. (Join-Path $PSScriptRoot 'uninstall.ps1')

function Assert-IRVIOServerBootstrapText([string]$Bootstrap){
 if($Bootstrap.Length -gt 24576 -or $Bootstrap -cnotmatch '\Airvio-bootstrap:v1:[A-Za-z0-9_-]+\z'){throw 'Invalid server bootstrap argument; use a complete IRVIO v1 bundle'}
}

function Assert-IRVIOServerPortsAvailable {
 foreach($port in @(4000,3478,51820)){
  $busy=@(if($port -eq 4000){Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue}else{Get-NetUDPEndpoint -LocalPort $port -ErrorAction SilentlyContinue})
  if($busy.Count){throw "Server port already in use: $port"}
 }
}

function Get-IRVIOServerCommand($Installation){
 Assert-IRVIOInstallationRecord $Installation.record 'server'
 $path=Join-Path $Installation.root 'server-bootstrap.txt'
 Assert-IRVIOInstallerACL $path $true
 $item=Get-Item -LiteralPath $path
 if($item -isnot [IO.FileInfo] -or $item.Length -gt 24576 -or (Get-FileHash $path).Hash -ine $Installation.record.bootstrap_sha256){throw 'Server bootstrap file differs from installation record'}
 $bootstrap=[IO.File]::ReadAllText($path)
 Assert-IRVIOServerBootstrapText $bootstrap
 return '"'+$Installation.program+'\daemon\irviod.exe" --role server --bootstrap '+$bootstrap
}

function Read-IRVIOPreservedServerInstallation([string]$Root,[string]$Program,[string]$Bootstrap){
 Assert-IRVIOInstallerACL $Root $true
 Assert-IRVIOServerBootstrapText $Bootstrap
 $receipt=Read-UninstallJournal (Join-Path $Root 'uninstalled.json') 'server'
 if($receipt.phase -cne 'completed'){throw 'Server removal is not completed'}
 $layout=[pscustomobject]@{root=$Root;program=$Program;record=$receipt.record}
 if((Get-IRVIOServerCommand $layout) -cne ('"'+$Program+'\daemon\irviod.exe" --role server --bootstrap '+$Bootstrap)){throw 'Server reinstall requires the retained bootstrap bundle'}
 if(Test-Path (Join-Path $Root 'desktop-user.sid')){throw 'Desktop authorization found in retained server state'}
 Read-IRVIOInstalledManifest (Join-Path $Root 'uninstall-manifest.json') $receipt.record 'server'|Out-Null
 if(-not $receipt.retain_profile){Assert-IRVIOErasedProfile $Root}
 return $receipt
}

function Read-IRVIOServerInstallation([switch]$Maintenance,[switch]$AllowSetupPending) {
 $principal=New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
 if(-not [Environment]::Is64BitProcess -or -not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Elevated 64-bit Windows installer required'}
 $root=Join-Path ([Environment]::GetFolderPath('CommonApplicationData')) 'IRVIO'
 $program=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO'
 $driver=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO Exit Driver'
 Assert-IRVIOInstallerACL $root $true
 $recordPath=Join-Path $root 'installation.json'
 Assert-IRVIOInstallerACL $recordPath $true
 if((Get-Item $recordPath).Length -gt 16KB){throw 'Oversized server installation record'}
 $record=Get-Content $recordPath -Raw|ConvertFrom-Json
 if($null -ne $record.PSObject.Properties['setup_pending'] -and -not $AllowSetupPending){throw 'Server installation is pending; rerun the same server installer before maintenance'}
 $installed=Read-IRVIOInstalledImage $root $program $driver $record (Join-Path $root 'geo') 'server'
 Assert-IRVIOInstallerACL (Join-Path $program 'daemon') $true
 Assert-IRVIOInstallerACL (Join-Path $root 'geo') $true
 $service=Get-UninstallSCMService 'IRVIO'
 $recovery=Get-UninstallSCMService 'IRVIO-Recovery'
 $kernel=Get-UninstallSCMService 'WinDivert'
 Assert-IRVIOServerServices $installed $service $recovery $kernel ([bool]$Maintenance)
 return $installed
}

function Assert-IRVIOServerSetupRecord($Record){
 Assert-IRVIOInstallationRecord $Record 'server'
 if($null -eq $Record.PSObject.Properties['setup_pending'] -or $Record.setup_pending -isnot [bool] -or -not $Record.setup_pending){throw 'Server setup receipt is missing or invalid'}
 foreach($name in @('setup_retained_receipt_sha256','setup_retained_manifest_sha256')){
  if($null -eq $Record.PSObject.Properties[$name] -or $Record.$name -isnot [string] -or $Record.$name -cnotmatch '^(|[a-f0-9]{64})$'){throw 'Invalid retained setup receipt binding'}
 }
 if([bool]$Record.setup_retained_receipt_sha256 -ne [bool]$Record.setup_retained_manifest_sha256){throw 'Incomplete retained setup receipt binding'}
}

function Get-IRVIOServerCopyFiles($Layout,$Manifest,[string]$Payload){
 foreach($file in $Manifest.files){
  $parts=$file.path.Split('/',2)
  if($parts[0] -ceq 'ui'){continue}
  if($file.path.EndsWith('.irvio-install-part',[StringComparison]::OrdinalIgnoreCase)){throw 'Payload uses reserved installation suffix'}
  $base=switch($parts[0]){'daemon'{Join-Path $Layout.program 'daemon'};'driver'{$Layout.driver};'geo'{Join-Path $Layout.root 'geo'};'licenses'{Join-Path $Layout.program 'licenses'};default{throw 'Unknown server payload component'}}
  [pscustomobject]@{source=(Join-Path $Payload $file.path);path=(Join-Path $base $parts[1]);sha256=$file.sha256;size=$file.size}
 }
 $manifestPath=Join-Path $Payload 'payload-manifest.json'
 [pscustomobject]@{source=$manifestPath;path=(Join-Path $Layout.program 'payload-manifest.json');sha256=$Layout.record.manifest_sha256;size=(Get-Item $manifestPath).Length}
}

function Assert-IRVIOServerPartialTree([string]$Directory,$Files){
 if(-not(Test-Path $Directory)){return}
 Assert-IRVIOInstallerACL $Directory $true
 $allowed=New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
 foreach($file in $Files){
  if(-not $file.path.StartsWith($Directory+'\',[StringComparison]::OrdinalIgnoreCase)){continue}
  [void]$allowed.Add($file.path);[void]$allowed.Add($file.path+'.irvio-install-part')
  $parent=[IO.Path]::GetDirectoryName($file.path)
  while($parent -ine $Directory){[void]$allowed.Add($parent);$parent=[IO.Path]::GetDirectoryName($parent)}
 }
 $pending=New-Object 'System.Collections.Generic.Stack[string]';$pending.Push($Directory);$count=0
 while($pending.Count){
  foreach($item in Get-ChildItem -LiteralPath $pending.Pop() -Force){
   if(++$count -gt 50000){throw 'Partial installation inventory exceeded bound'}
   Assert-IRVIOInstallerACL $item.FullName
   if(-not $allowed.Contains($item.FullName)){throw 'Unowned object in partial server installation'}
   if($item.PSIsContainer){$pending.Push($item.FullName)}
  }
 }
}

function Copy-IRVIOServerFile($File,[bool]$InterruptPartial=$false){
 $target=$File.path;$temporary=$target+'.irvio-install-part'
 $parent=[IO.Path]::GetDirectoryName($target)
 if(-not(Test-Path $parent)){New-Item -ItemType Directory -Path $parent -Force|Out-Null}
 Assert-IRVIOInstallerACL $parent
 if(Test-Path $target){
  Assert-IRVIOInstallerACL $target $true
  $item=Get-Item -LiteralPath $target
  if($item -isnot [IO.FileInfo] -or $item.Length -ne $File.size -or (Get-FileHash $target).Hash -ine $File.sha256){throw 'Completed server copy differs from expected payload'}
  if(Test-Path $temporary){throw 'Both completed and partial server copy exist; inspect before continuing'}
  return
 }
 if(Test-Path $temporary){
  Assert-IRVIOInstallerACL $temporary
  if((Get-Item $temporary) -isnot [IO.FileInfo]){throw 'Partial server copy is not a file'}
  [IO.File]::SetAttributes($temporary,([IO.File]::GetAttributes($temporary) -band (-bnot [IO.FileAttributes]::ReadOnly)))
 }
 if($InterruptPartial){
  $source=[IO.File]::OpenRead($File.source)
  try{$bytes=New-Object byte[] 64;$length=$source.Read($bytes,0,$bytes.Length);$part=[IO.File]::Open($temporary,[IO.FileMode]::Create,[IO.FileAccess]::Write,[IO.FileShare]::None);try{$part.Write($bytes,0,$length);$part.Flush($true)}finally{$part.Dispose()}}finally{$source.Dispose()}
  Protect-UpdateFile $temporary
  throw 'IRVIO development interruption: copy-partial'
 }
 Copy-Item -LiteralPath $File.source -Destination $temporary -Force
 $item=Get-Item $temporary
 if($item.Length -ne $File.size -or (Get-FileHash $temporary).Hash -ine $File.sha256){throw 'Server source changed during copy'}
 Protect-UpdateFile $temporary
 [IO.File]::SetAttributes($temporary,([IO.File]::GetAttributes($temporary) -band (-bnot [IO.FileAttributes]::ReadOnly)))
 $stream=[IO.File]::Open($temporary,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
 try{$stream.Flush($true)}finally{$stream.Dispose()}
 [IO.File]::Move($temporary,$target)
}

function Read-IRVIOServerCopyRecord([string]$Root,[string]$ManifestSHA256,[string]$BootstrapSHA256,[bool]$Development){
 Assert-IRVIOInstallerACL $Root $true
 $path=Join-Path $Root 'server-copy.json'
 Assert-IRVIOInstallerACL $path $true
 if((Get-Item $path).Length -gt 16KB){throw 'Oversized server copy record'}
 $record=Get-Content $path -Raw|ConvertFrom-Json
 Assert-IRVIOServerSetupRecord $record
 if($record.manifest_sha256 -ine $ManifestSHA256 -or $record.bootstrap_sha256 -cne $BootstrapSHA256 -or $record.development_unsigned -ne $Development){throw 'Partial server copy requires the original payload/bootstrap/signing mode'}
 return $record
}

function Assert-IRVIOServerCopyInstallationRecord($Layout){
 $path=Join-Path $Layout.root 'installation.json'
 if(-not(Test-Path $path)){return}
 Assert-IRVIOInstallerACL $path $true
 if((Get-Item $path).Length -gt 16KB){throw 'Oversized installation record during server copy'}
 $existing=Get-Content $path -Raw|ConvertFrom-Json
 Assert-IRVIOServerSetupRecord $existing
 foreach($name in @('manifest_sha256','bootstrap_sha256','development_unsigned','setup_retained_receipt_sha256','setup_retained_manifest_sha256')){if($existing.$name -cne $Layout.record.$name){throw 'Server copy conflicts with installation record'}}
}

function Complete-IRVIOServerCopy($Layout,$Manifest,[string]$Payload,[string]$Bootstrap,[string]$DevelopmentStopAfter=''){
 Assert-IRVIOServerSetupRecord $Layout.record
 if($DevelopmentStopAfter -and -not $Layout.record.development_unsigned){throw 'Copy interruption requires a development payload'}
 foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Partial server copy refuses existing services'}}
 foreach($name in @('maintenance.json','uninstall.json','software-update','desktop-user.sid')){if(Test-Path (Join-Path $Layout.root $name)){throw 'Partial server copy conflicts with other lifecycle state'}}
 if(Test-Path (Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk')){throw 'Partial server copy conflicts with desktop shortcut'}
 Assert-IRVIOServerSetupRetainedFiles $Layout
 Assert-IRVIOServerCopyInstallationRecord $Layout
 if($Layout.record.setup_retained_receipt_sha256){Read-IRVIOPreservedServerInstallation $Layout.root $Layout.program $Bootstrap|Out-Null}
 $files=@(Get-IRVIOServerCopyFiles $Layout $Manifest $Payload)
 foreach($path in @($Layout.program,$Layout.driver,(Join-Path $Layout.root 'geo'))){Assert-IRVIOServerPartialTree $path $files}
 foreach($file in $files){
  if(Test-Path $file.path){
   Assert-IRVIOInstallerACL $file.path $true
   $item=Get-Item $file.path
   if($item -isnot [IO.FileInfo] -or $item.Length -ne $file.size -or (Get-FileHash $file.path).Hash -ine $file.sha256){throw 'Completed server copy differs from expected payload'}
   if(Test-Path ($file.path+'.irvio-install-part')){throw 'Both completed and partial server copy exist; inspect before continuing'}
  }
 }
 Assert-IRVIOServerPortsAvailable
 if($DevelopmentStopAfter -ceq 'copy-intent'){throw 'IRVIO development interruption: copy-intent'}
 foreach($path in @($Layout.program,$Layout.driver,(Join-Path $Layout.root 'geo'),(Join-Path $Layout.program 'daemon'))){if(-not(Test-Path $path)){New-IRVIOServerDirectory $path}}
 $first=$true
 foreach($file in $files){
  Copy-IRVIOServerFile $file ($first -and $DevelopmentStopAfter -ceq 'copy-partial')
  if($first -and $DevelopmentStopAfter -ceq 'copy-file'){throw 'IRVIO development interruption: copy-file'}
  $first=$false
 }
 $bootstrapPath=Join-Path $Layout.root 'server-bootstrap.txt'
 if(Test-Path $bootstrapPath){
  if(Test-Path ($bootstrapPath+'.irvio-install-part')){throw 'Both completed and partial bootstrap files exist'}
  Assert-IRVIOInstallerACL $bootstrapPath $true
  if((Get-FileHash $bootstrapPath).Hash -ine $Layout.record.bootstrap_sha256){throw 'Server bootstrap changed during copy'}
 }else{
  $temporary=$bootstrapPath+'.irvio-install-part'
  if(Test-Path $temporary){Assert-IRVIOInstallerACL $temporary}
  [IO.File]::WriteAllText($temporary,$Bootstrap,[Text.Encoding]::ASCII)
  Protect-UpdateFile $temporary
  [IO.File]::Move($temporary,$bootstrapPath)
 }
 Read-IRVIOInstalledImage $Layout.root $Layout.program $Layout.driver $Layout.record (Join-Path $Layout.root 'geo') 'server'|Out-Null
 if($DevelopmentStopAfter -ceq 'copy-complete'){throw 'IRVIO development interruption: copy-complete'}
 $recordPath=Join-Path $Layout.root 'installation.json'
 Assert-IRVIOServerCopyInstallationRecord $Layout
 if(-not(Test-Path $recordPath)){
  $temporary=$recordPath+'.new'
  if(Test-Path $temporary){Assert-IRVIOInstallerACL $temporary;[IO.File]::Move($temporary,(Join-Path $Layout.root ('setup-journal-interrupted-'+[Guid]::NewGuid().ToString('N')+'.json')))}
  Write-UpdateJSON $recordPath $Layout.record
 }
 if($DevelopmentStopAfter -ceq 'copy-record'){throw 'IRVIO development interruption: copy-record'}
 Remove-Item -LiteralPath (Join-Path $Layout.root 'server-copy.json')
}

function Assert-IRVIOServerSetupServices($Installed,$Service,$Recovery,$Kernel){
 $command=Get-IRVIOServerCommand $Installed
 if($null -ne $Service -and ($Service.Path -cne $command -or $Service.Account -ine 'LocalSystem' -or $Service.Type -ne 16 -or $Service.Start -notin @(2,4) -or $Service.State -notin @(1,2,4))){throw 'Pending server service ownership/start state differs'}
 if($null -ne $Recovery -and ($Recovery.Path -cne ('"'+$Installed.program+'\daemon\irviod.exe" --boot-recover') -or $Recovery.Account -ine 'LocalSystem' -or $Recovery.Type -ne 16 -or $Recovery.Start -ne 3 -or $Recovery.State -ne 1)){throw 'Pending server recovery service ownership/start state differs'}
 $driver=Join-Path $Installed.driver 'WinDivert64.sys'
 if($null -ne $Kernel -and ($Kernel.Path -cnotin @($driver,('"'+$driver+'"'),('\??\'+$driver)) -or $Kernel.Type -ne 1 -or $Kernel.Start -ne 3 -or $Kernel.State -notin @(1,4))){throw 'Pending server driver ownership/start state differs'}
 if($null -ne $Service -and $Service.State -ne 1 -and ($null -eq $Recovery -or $null -eq $Kernel)){throw 'Running pending server has an incomplete service set; retain state for inspection'}
}

function Assert-IRVIOServerSetupRetainedFiles($Installed){
 foreach($entry in @(@('uninstalled.json','setup_retained_receipt_sha256'),@('uninstall-manifest.json','setup_retained_manifest_sha256'))){
  $path=Join-Path $Installed.root $entry[0]
  if(Test-Path $path){
   Assert-IRVIOInstallerACL $path $true
   $expected=$Installed.record.($entry[1])
   if(-not $expected -or (Get-FileHash $path).Hash -ine $expected){throw 'Retained uninstall metadata changed during pending setup'}
  }
 }
}

function Complete-IRVIOServerSetup($Installed,[string]$DevelopmentStopAfter=''){
 Assert-IRVIOServerSetupRecord $Installed.record
 if($DevelopmentStopAfter -cnotin @('','record','kernel','recovery','service','configured','started') -or ($DevelopmentStopAfter -and -not $Installed.record.development_unsigned)){throw 'Invalid development installation interruption'}
 $root=$Installed.root;$program=$Installed.program;$driver=$Installed.driver
 foreach($name in @('maintenance.json','uninstall.json','software-update','desktop-user.sid','server-copy.json')){if(Test-Path (Join-Path $root $name)){throw 'Pending setup conflicts with maintenance or desktop state'}}
 if(Test-Path (Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk')){throw 'Pending server setup conflicts with desktop shortcut'}
 Assert-IRVIOServerSetupRetainedFiles $Installed
 $service=Get-UninstallSCMService IRVIO
 $recovery=Get-UninstallSCMService IRVIO-Recovery
 $kernel=Get-UninstallSCMService WinDivert
 Assert-IRVIOServerSetupServices $Installed $service $recovery $kernel
 if($null -eq $service -or $service.State -eq 1){Assert-IRVIOServerPortsAvailable}
 if($DevelopmentStopAfter -ceq 'record'){throw 'IRVIO development interruption: record'}
 if($null -eq $kernel){Invoke-IRVIOMaintenanceSC @('create','WinDivert','type=','kernel','start=','demand','binPath=',('"'+$driver+'\WinDivert64.sys"'))}
 if($DevelopmentStopAfter -ceq 'kernel'){throw 'IRVIO development interruption: kernel'}
 if($null -eq $recovery){New-Service -Name IRVIO-Recovery -BinaryPathName ('"'+$program+'\daemon\irviod.exe" --boot-recover') -StartupType Manual -DisplayName 'IRVIO boot recovery'|Out-Null}
 if($DevelopmentStopAfter -ceq 'recovery'){throw 'IRVIO development interruption: recovery'}
 if($null -eq $service){New-Service -Name IRVIO -BinaryPathName (Get-IRVIOServerCommand $Installed) -StartupType Disabled -DisplayName 'IRVIO Server and Exit Node' -DependsOn WinDivert|Out-Null}
 if($DevelopmentStopAfter -ceq 'service'){throw 'IRVIO development interruption: service'}
 Invoke-IRVIOMaintenanceSC @('sdset','IRVIO','D:P(A;;GA;;;SY)(A;;GA;;;BA)')
 Invoke-IRVIOMaintenanceSC @('failure','IRVIO','reset=','86400','actions=','restart/1000/restart/3000')
 Invoke-IRVIOMaintenanceSC @('failureflag','IRVIO','1')
 Read-IRVIOServerInstallation -AllowSetupPending|Out-Null
 if($DevelopmentStopAfter -ceq 'configured'){throw 'IRVIO development interruption: configured'}
 Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','auto')
 $service=Get-UninstallSCMService IRVIO
 if($service.State -eq 1){Invoke-IRVIOMaintenanceSC @('start','IRVIO')}
 $watch=[Diagnostics.Stopwatch]::StartNew()
 do{
  $service=Get-UninstallSCMService IRVIO
  if($service.State -eq 1){throw 'Server stopped during startup; inspect ProgramData\IRVIO\logs and SCM events'}
  if($watch.Elapsed.TotalSeconds -ge 60){throw 'Server startup exceeded 60 seconds; inspect SCM and protected daemon logs'}
  if($service.State -ne 4){Start-Sleep -Milliseconds 100}
 }while($service.State -ne 4)
 if($DevelopmentStopAfter -ceq 'started'){throw 'IRVIO development interruption: started'}
 Read-IRVIOServerInstallation -AllowSetupPending|Out-Null
 Assert-IRVIOServerSetupRetainedFiles $Installed
 foreach($name in @('uninstall-manifest.json','uninstalled.json')){
  $path=Join-Path $root $name
  if(Test-Path $path){Remove-Item -LiteralPath $path}
 }
 $record=$Installed.record|ConvertTo-Json|ConvertFrom-Json
 foreach($name in @('setup_pending','setup_retained_receipt_sha256','setup_retained_manifest_sha256')){$record.PSObject.Properties.Remove($name)}
 $temporary=Join-Path $root 'installation.json.new'
 if(Test-Path $temporary){
  Assert-IRVIOInstallerACL $temporary
  [IO.File]::Move($temporary,(Join-Path $root ('setup-journal-interrupted-'+[Guid]::NewGuid().ToString('N')+'.json')))
 }
 Write-UpdateJSON (Join-Path $root 'installation.json') $record
 Read-IRVIOServerInstallation|Out-Null
}

function Assert-IRVIOServerServices($installed,$service,$recovery,$kernel,[bool]$Maintenance=$false){
 $command=Get-IRVIOServerCommand $installed
 $program=$installed.program;$driver=$installed.driver
 if($null -eq $service -or $service.Path -cne $command -or $service.Account -ine 'LocalSystem' -or $service.Type -ne 16){throw 'Server SCM ownership differs from installation'}
 $recoveryStarts=if($Maintenance){@(3,4)}else{@(3)}
 if($null -eq $recovery -or $recovery.Path -cne ('"'+$program+'\daemon\irviod.exe" --boot-recover') -or $recovery.Account -ine 'LocalSystem' -or $recovery.Type -ne 16 -or $recovery.Start -notin $recoveryStarts){throw 'Server recovery service must be owned and demand-start (or maintenance-disabled)'}
 if($null -eq $kernel -or $kernel.Path -cnotin @((Join-Path $driver 'WinDivert64.sys'),('"'+$driver+'\WinDivert64.sys"'),('\??\'+$driver+'\WinDivert64.sys')) -or $kernel.Type -ne 1 -or $kernel.Start -ne 3){throw 'Server WinDivert registration is not owned'}
}

function New-IRVIOServerDirectory([string]$Path){
 Assert-IRVIOPlainPath ([IO.Path]::GetDirectoryName($Path))
 if(-not ('IRVIOServer.NativeDirectory' -as [type])){
  Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
namespace IRVIOServer {
 public static class NativeDirectory {
  [StructLayout(LayoutKind.Sequential)] struct Attributes { public int Length; public IntPtr Descriptor; public int Inherit; }
  [DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool ConvertStringSecurityDescriptorToSecurityDescriptor(string text,uint revision,out IntPtr descriptor,out uint size);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool CreateDirectory(string path,ref Attributes attributes);
  [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr memory);
  public static void Create(string path) {
   IntPtr descriptor; uint size;
   if(!ConvertStringSecurityDescriptorToSecurityDescriptor("O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",1,out descriptor,out size)) throw new Win32Exception(Marshal.GetLastWin32Error());
   try {
    var attributes=new Attributes { Length=Marshal.SizeOf(typeof(Attributes)),Descriptor=descriptor,Inherit=0 };
    if(!CreateDirectory(path,ref attributes)) throw new Win32Exception(Marshal.GetLastWin32Error());
   } finally { LocalFree(descriptor); }
  }
 }
}
'@
 }
 [IRVIOServer.NativeDirectory]::Create($Path)
}
