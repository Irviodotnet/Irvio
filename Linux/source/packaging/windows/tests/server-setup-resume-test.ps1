# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Payload,[Parameter(Mandatory=$true)][string]$DifferentPayload,[Parameter(Mandatory=$true)][string]$Evidence,[ValidateSet('record','kernel','recovery','service','configured','started','copy-intent','copy-partial','copy-file','copy-complete','copy-record')][string[]]$Phases=@('record','kernel','recovery','service','configured','started'))
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
New-IRVIOServerDirectory $Evidence
$installed=Read-IRVIOServerInstallation
$root=$installed.root;$program=$installed.program
if((Get-UninstallSCMService IRVIO).State -ne 1 -or (Get-UninstallSCMService IRVIO).Start -ne 4){throw 'Stopped disabled disposable server required'}
$bootstrap=[IO.File]::ReadAllText("$root\server-bootstrap.txt")
$identity=(Get-FileHash "$root\profile\identity.key").Hash
$bootstrapHash=(Get-FileHash "$root\server-bootstrap.txt").Hash
$manifestHash=(Get-FileHash "$Payload\payload-manifest.json").Hash
$differentHash=(Get-FileHash "$DifferentPayload\payload-manifest.json").Hash
if($differentHash -eq $manifestHash){throw 'Different payload fixture required'}
$probe=Join-Path $Payload 'daemon\irviod.exe'
$results=New-Object 'System.Collections.Generic.List[object]'
function Reject([string]$Name,[scriptblock]$Action,[string]$Pattern){
 $caught=$null;try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Pattern){throw "Wrong refusal for ${Name}: $caught"}
 $results.Add(@{test=$Name;result='PASS'})
}
function Assert-Identity {
 if((Get-FileHash "$root\profile\identity.key").Hash -cne $identity -or (Get-FileHash "$root\server-bootstrap.txt").Hash -cne $bootstrapHash){throw 'Setup changed retained identity/bootstrap'}
}
function Set-FixtureServiceCommand([string]$Command){
 $service=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
 if($service.State -ne 'Stopped' -or $service.StartMode -ne 'Disabled'){throw 'Only stopped disabled fixture service may be altered'}
 $result=Invoke-CimMethod -InputObject $service -MethodName Change -Arguments @{PathName=$Command}
 if($result.ReturnValue -ne 0){throw "Fixture SCM command change failed: $($result.ReturnValue)"}
 if((Get-UninstallSCMService IRVIO).Path -cne $Command){throw 'Fixture SCM command was not applied exactly'}
}
foreach($phase in $Phases){
 & "$Scripts\uninstall-desktop.ps1" -Role server -Payload $Payload -ManifestSHA256 $manifestHash -DevelopmentUnsigned *> "$Evidence\$phase-uninstall.log"
 Assert-Identity
 Reject "$phase-interrupted" {& "$Scripts\install-server.ps1" -Payload $Payload -ManifestSHA256 $manifestHash -Bootstrap $bootstrap -DevelopmentUnsigned -DevelopmentStopAfter $phase} "IRVIO development interruption: $phase"
 $copyPhase=$phase.StartsWith('copy-')
 $recordPath=if($copyPhase){"$root\server-copy.json"}else{"$root\installation.json"}
 $pending=Get-Content $recordPath -Raw|ConvertFrom-Json
 Assert-IRVIOServerSetupRecord $pending
 if($copyPhase){
  foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Copy phase prematurely created a service'}}
  $results.Add(@{test="$phase-no-services";result='PASS'})
 }else{Reject "$phase-maintenance-refused" {Read-IRVIOServerInstallation} 'installation is pending'}
 $recordHash=(Get-FileHash $recordPath).Hash
 $bootstrapPattern=if($copyPhase){'original payload/bootstrap/signing mode'}else{'existing bootstrap bundle'}
 Reject "$phase-wrong-bootstrap-refused" {& "$Scripts\install-or-update-server.ps1" -Payload $Payload -ManifestSHA256 $manifestHash -Bootstrap 'irvio-bootstrap:v1:QUJD' -DevelopmentUnsigned} $bootstrapPattern
 Reject "$phase-wrong-payload-refused" {& "$Scripts\install-or-update-server.ps1" -Payload $DifferentPayload -ManifestSHA256 $differentHash -Bootstrap $bootstrap -DevelopmentUnsigned} 'original payload'
 if((Get-FileHash $recordPath).Hash -cne $recordHash){throw 'Refused resume modified pending receipt'}
 if($phase -ceq 'copy-file'){
  $foreign=Join-Path $program 'foreign-fixture.txt'
  [IO.File]::WriteAllText($foreign,'must survive refusal')
  try{Reject 'partial-foreign-file-refused' {& "$Scripts\install-or-update-server.ps1" -Payload $Payload -ManifestSHA256 $manifestHash -Bootstrap $bootstrap -DevelopmentUnsigned} 'Unowned object';if([IO.File]::ReadAllText($foreign) -cne 'must survive refusal'){throw 'Refusal changed foreign file'}}
  finally{Remove-Item -LiteralPath $foreign}
 }
 if($phase -ceq 'record'){
  $license=Join-Path $program 'licenses\MSVC-LICENSE.txt'
  [IO.File]::AppendAllText($license,'fixture corruption')
  try{Reject 'modified-payload-refused' {& "$Scripts\install-or-update-server.ps1" -Payload $Payload -ManifestSHA256 $manifestHash -Bootstrap $bootstrap -DevelopmentUnsigned} 'Installed file changed'}
  finally{Copy-Item -LiteralPath (Join-Path $Payload 'licenses\MSVC-LICENSE.txt') -Destination $license -Force}
  foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Refused payload repair created a service'}}
 }
 if($phase -ceq 'service'){
  $command=(Get-UninstallSCMService IRVIO).Path
  Set-FixtureServiceCommand ('"'+$program+'\daemon\irviod.exe" --role server')
  try{Reject 'foreign-scm-refused' {& "$Scripts\install-or-update-server.ps1" -Payload $Payload -ManifestSHA256 $manifestHash -Bootstrap $bootstrap -DevelopmentUnsigned} 'ownership/start state'}
  finally{Set-FixtureServiceCommand $command}
 }
 $priorPID=0
 if($phase -ceq 'started'){$priorPID=(Get-UninstallSCMService IRVIO).ProcessId;if(-not $priorPID){throw 'Expected started server PID'}}
 $partial='{interrupted setup commit'
 $priorJournals=@{}
 if($phase -in @('configured','copy-complete')){
  foreach($saved in Get-ChildItem $root -Filter 'setup-journal-interrupted-*.json'){$priorJournals[$saved.Name]=(Get-FileHash $saved.FullName).Hash}
  [IO.File]::WriteAllText("$root\installation.json.new",$partial,[Text.Encoding]::UTF8)
 }
 & "$Scripts\install-or-update-server.ps1" -Payload $Payload -ManifestSHA256 $manifestHash -Bootstrap $bootstrap -DevelopmentUnsigned *> "$Evidence\$phase-resume.log"
 $installed=Read-IRVIOServerInstallation
 if($null -ne $installed.record.PSObject.Properties['setup_pending']){throw 'Pending marker retained'}
 Assert-Identity
 foreach($name in @('uninstalled.json','uninstall-manifest.json','installation.json.new','server-copy.json')){if(Test-Path (Join-Path $root $name)){throw 'Setup transaction residue'}}
 if($phase -in @('configured','copy-complete')){
  foreach($name in $priorJournals.Keys){if((Get-FileHash (Join-Path $root $name)).Hash -cne $priorJournals[$name]){throw 'Prior journal evidence changed'}}
  $saved=@(Get-ChildItem $root -Filter 'setup-journal-interrupted-*.json'|Where-Object {-not $priorJournals.ContainsKey($_.Name)})
  if($saved.Count -ne 1 -or [IO.File]::ReadAllText($saved[0].FullName) -cne $partial){throw 'Interrupted journal bytes were not retained exactly once'}
 }
 $watch=[Diagnostics.Stopwatch]::StartNew()
 do{
  $service=Get-UninstallSCMService IRVIO
  if($service.State -ne 4 -or $service.Start -ne 2){throw 'Resumed server is not running with automatic startup'}
  if($priorPID -and $service.ProcessId -ne $priorPID){throw 'Completion restarted already-running server'}
  $tcp=@(Get-NetTCPConnection -State Listen -LocalPort 4000 -ErrorAction SilentlyContinue|Where-Object OwningProcess -eq $service.ProcessId)
  $stun=@(Get-NetUDPEndpoint -LocalPort 3478 -ErrorAction SilentlyContinue|Where-Object OwningProcess -eq $service.ProcessId)
  $awg=@(Get-NetUDPEndpoint -LocalPort 51820 -ErrorAction SilentlyContinue|Where-Object OwningProcess -eq $service.ProcessId)
  if($tcp.Count -and $stun.Count -and $awg.Count){break}
  if($watch.Elapsed.TotalSeconds -gt 60){throw 'Resumed fixed-port services not ready'}
  Start-Sleep -Milliseconds 200
 }while($true)
 $pidBeforeRepeat=$service.ProcessId
 & "$Scripts\install-or-update-server.ps1" -Payload $Payload -ManifestSHA256 $manifestHash -Bootstrap $bootstrap -DevelopmentUnsigned *> "$Evidence\$phase-repeat.log"
 if((Get-UninstallSCMService IRVIO).ProcessId -ne $pidBeforeRepeat){throw 'Idempotent setup restarted server'}
 Stop-IRVIOForMaintenance -Role server|Out-Null
 try{Assert-IRVIOMaintenanceClean $probe (Get-FileHash $probe).Hash -Role server|ConvertTo-Json|Set-Content "$Evidence\$phase-cleanup.json"}
 finally{Restore-IRVIOMaintenanceStartModes -Role server|Out-Null}
 Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','disabled')
 $results.Add(@{test="$phase-resume-fixed-ports-identity-cleanup";result='PASS';already_running=($priorPID -ne 0)})
 $results|ConvertTo-Json|Set-Content "$Evidence\results.json"
}
Read-IRVIOServerInstallation|Out-Null
Assert-Identity
"SERVER SETUP RESUME PASS: $($Phases.Count) phases; arbitrary power-loss gates remain separate"
