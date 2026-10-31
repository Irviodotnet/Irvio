# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

. (Join-Path $PSScriptRoot 'installation.ps1')

function Invoke-IRVIOMaintenanceSC([string[]]$Arguments) {
 & "$env:SystemRoot\System32\sc.exe" @Arguments | Out-Null
 if($LASTEXITCODE){throw "Maintenance SCM operation failed ($($Arguments -join ' ')): $LASTEXITCODE"}
}

function Get-IRVIOMaintenanceStart($Service) {
 switch($Service.StartMode){
  'Manual' {return 'demand'}
  'Disabled' {return 'disabled'}
  'Auto' {if($Service.DelayedAutoStart){return 'delayed-auto'};return 'auto'}
  default {throw 'Unsupported service start mode'}
 }
}

function Assert-IRVIOMaintenanceRecord($Record,$Installation) {
 if($Record.manifest_sha256 -cne $Installation.record.manifest_sha256){throw 'Maintenance record does not match installed ownership'}
 if($Installation.record.role -ceq 'server'){
  if($Record.schema -ne 2 -or $Record.role -cne 'server' -or $Record.bootstrap_sha256 -cne $Installation.record.bootstrap_sha256){throw 'Maintenance record does not match installed server ownership'}
 }elseif($Record.schema -ne 1 -or $Record.desktop_sid -cne $Installation.record.desktop_sid){throw 'Maintenance record does not match installed ownership'}
 if($Record.services.Count -ne 2){throw 'Invalid maintenance service count'}
 for($i=0;$i -lt 2;$i++){
  $row=$Record.services[$i]
  if($row.name -cne @('IRVIO','IRVIO-Recovery')[$i] -or $row.start -cnotin @('demand','disabled','auto','delayed-auto') -or $row.was_running -isnot [bool]){throw 'Invalid maintenance service record'}
 }
}

function Read-IRVIOMaintenanceRecord($Installation) {
 $path=Join-Path $Installation.root 'maintenance.json'
 Assert-IRVIOInstallerACL $path
 if((Get-Item -LiteralPath $path).Length -gt 16KB){throw 'Oversized maintenance record'}
 $record=Get-Content -LiteralPath $path -Raw|ConvertFrom-Json
 Assert-IRVIOMaintenanceRecord $record $Installation
 return $record
}

function Open-IRVIOMaintenanceLock($Installation) {
 $path=Join-Path $Installation.root 'maintenance.lock'
 if(Test-Path -LiteralPath $path){Assert-IRVIOInstallerACL $path}
 $file=[IO.File]::Open($path,[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
 try{Assert-IRVIOInstallerACL $path}catch{$file.Dispose();throw}
 return $file
}

function Read-IRVIOMaintenanceInstallation([ValidateSet('desktop','server')][string]$Role='desktop'){
 if($Role -ceq 'server'){return Read-IRVIOServerInstallation -Maintenance}
 return Read-IRVIOInstallation
}

function Stop-IRVIOForMaintenance([ValidateRange(1,180)][int]$TimeoutSeconds=60,[ValidateSet('desktop','server')][string]$Role='desktop') {
 $installed=Read-IRVIOMaintenanceInstallation $Role
 $lock=Open-IRVIOMaintenanceLock $installed
 try{
 $recordPath=Join-Path $installed.root 'maintenance.json'
 if(Test-Path -LiteralPath $recordPath){throw 'Pending maintenance exists; inspect and restore it before another operation'}
 $rows=@()
 foreach($name in @('IRVIO','IRVIO-Recovery')){
  $service=Get-CimInstance Win32_Service -Filter "Name='$name'"
  if($service.State -notin @('Stopped','Running')){throw 'Service is transitioning; retry after it settles'}
  $rows+=@{name=$name;start=(Get-IRVIOMaintenanceStart $service);was_running=($service.State -eq 'Running')}
 }
 $record=if($Role -ceq 'server'){@{schema=2;role='server';manifest_sha256=$installed.record.manifest_sha256;bootstrap_sha256=$installed.record.bootstrap_sha256;services=$rows}}else{@{schema=1;manifest_sha256=$installed.record.manifest_sha256;desktop_sid=$installed.record.desktop_sid;services=$rows}}
 Assert-IRVIOMaintenanceRecord $record $installed
 $bytes=[Text.Encoding]::UTF8.GetBytes(($record|ConvertTo-Json -Depth 4))
 $file=[IO.File]::Open($recordPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
 try{$file.Write($bytes,0,$bytes.Length);$file.Flush($true)}finally{$file.Dispose()}
 Assert-IRVIOInstallerACL $recordPath
 # A retained record fences interrupted maintenance; never kill a daemon or erase its protection.
 $watch=[Diagnostics.Stopwatch]::StartNew()
 foreach($row in $rows){
  # Keep recovery automatic until the daemon has completed its WFP teardown.
  Invoke-IRVIOMaintenanceSC @('config',$row.name,'start=','disabled')
  $service=Get-CimInstance Win32_Service -Filter "Name='$($row.name)'"
  if($service.State -eq 'Running'){Invoke-IRVIOMaintenanceSC @('stop',$row.name)}
  do{
   $service=Get-CimInstance Win32_Service -Filter "Name='$($row.name)'"
   if($null -eq $service){throw 'Owned service disappeared during maintenance'}
   if($watch.Elapsed.TotalSeconds -ge $TimeoutSeconds){throw 'Graceful service stop timed out; retained maintenance record and protection'}
   if($service.State -ne 'Stopped'){Start-Sleep -Milliseconds 100}
  }while($service.State -ne 'Stopped')
  if($service.ProcessId -ne 0 -or ($service.ExitCode -ne 0 -and ($row.was_running -or $service.ExitCode -ne 1077))){throw 'Service stopped with a failure; inspect runtime before maintenance'}
 }
 $daemon=Join-Path $installed.program 'daemon\irviod.exe'
 if(Get-CimInstance Win32_Process -Filter "Name='irviod.exe'"|Where-Object {$_.ExecutablePath -ieq $daemon}){throw 'Installed daemon process remains after SCM stop'}
 $after=Read-IRVIOMaintenanceInstallation $Role
 if($after.record.manifest_sha256 -cne $record.manifest_sha256){throw 'Installation changed during maintenance'}
 return $record
 }finally{$lock.Dispose()}
}

function Restore-IRVIOMaintenanceStartModes([ValidateSet('desktop','server')][string]$Role='desktop') {
 $installed=Read-IRVIOMaintenanceInstallation $Role
 $lock=Open-IRVIOMaintenanceLock $installed
 try{
 $record=Read-IRVIOMaintenanceRecord $installed
 foreach($row in $record.services){Invoke-IRVIOMaintenanceSC @('config',$row.name,'start=',$row.start)}
 foreach($row in $record.services){
  $actual=Get-CimInstance Win32_Service -Filter "Name='$($row.name)'"
  if((Get-IRVIOMaintenanceStart $actual) -cne $row.start){throw 'Start-mode restoration did not persist'}
 }
 Remove-Item -LiteralPath (Join-Path $installed.root 'maintenance.json')
 # Restoring start modes deliberately does not revive a user's disconnected VPN session.
 return $record
 }finally{$lock.Dispose()}
}

function Assert-IRVIOMaintenanceClean([string]$ProbePath,[string]$ProbeSHA256,[ValidateSet('desktop','server')][string]$Role='desktop') {
 $installed=Read-IRVIOMaintenanceInstallation $Role
 $lock=Open-IRVIOMaintenanceLock $installed
 try{
 return Assert-IRVIOMaintenanceCleanLocked $installed $ProbePath $ProbeSHA256
 }finally{$lock.Dispose()}
}

function Invoke-IRVIOMaintenanceProbe([string]$ProbePath,[string]$ProbeSHA256,[ValidateSet('check','remove-profile')][string]$Operation='check') {
 if($ProbeSHA256 -notmatch '^[a-fA-F0-9]{64}$'){throw 'Trusted maintenance probe digest required'}
 Assert-IRVIOInstallerACL $ProbePath
 $probe=[IO.File]::Open($ProbePath,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::Read)
 try{
  if((Get-FileHash -InputStream $probe).Hash -ine $ProbeSHA256){throw 'Maintenance probe digest mismatch'}
  $start=New-Object Diagnostics.ProcessStartInfo
  $start.FileName=(Get-Item -LiteralPath $ProbePath).FullName
  $start.Arguments='--maintenance-'+$Operation;$start.UseShellExecute=$false;$start.CreateNoWindow=$true
  $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
  $process=New-Object Diagnostics.Process
  $process.StartInfo=$start
  try{
   if(-not $process.Start()){throw 'Maintenance probe did not start'}
   $stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
   if(-not $process.WaitForExit(30000)){
    $process.Kill()
    throw "Maintenance $Operation timed out; transaction record retained for inspection/reentry"
   }
   $output=$stdout.GetAwaiter().GetResult();$diagnostic=$stderr.GetAwaiter().GetResult()
   $expected=if($Operation -ceq 'check'){'IRVIO_MAINTENANCE_CORE_CLEAN_V1'}else{'IRVIO_MAINTENANCE_PROFILE_REMOVED_V1'}
   if($process.ExitCode -ne 0 -or $output.Trim() -cne $expected){throw "Core maintenance $Operation refused: $diagnostic"}
  }finally{$process.Dispose()}
 }finally{$probe.Dispose()}
}

function Assert-IRVIOMaintenanceCleanLocked($installed,[string]$ProbePath,[string]$ProbeSHA256) {
 Read-IRVIOMaintenanceRecord $installed|Out-Null
 foreach($name in @('IRVIO','IRVIO-Recovery')){
  $service=Get-CimInstance Win32_Service -Filter "Name='$name'"
  if($service.State -ne 'Stopped' -or $service.StartMode -ne 'Disabled'){throw 'Maintenance services are not fenced'}
 }
 Invoke-IRVIOMaintenanceProbe $ProbePath $ProbeSHA256
 $adapters=@(Get-NetAdapter -IncludeHidden -ErrorAction Stop|Where-Object {$_.Name -ieq 'irvio0'})
 if($adapters.Count){throw 'Product tunnel adapter remains; cleanup required'}
 $rules=@(Get-NetFirewallRule -PolicyStore ActiveStore -ErrorAction Stop|Where-Object {$_.Group -in @('IRVIO.Control.v1','IRVIO.Inbound.v1') -or $_.DisplayName -like 'IRVIO.Control.*' -or $_.DisplayName -like 'IRVIO.Inbound.*'})
 if($rules.Count){throw 'IRVIO firewall rules remain; cleanup required'}
 return [pscustomobject]@{core=$true;adapters_absent=$true;firewall_rules_absent=$true;manifest_sha256=$installed.record.manifest_sha256}
}
