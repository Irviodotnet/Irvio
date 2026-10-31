# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Scripts,
 [Parameter(Mandatory=$true)][string]$SetupEXE,
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$API,
 [Parameter(Mandatory=$true)][string]$BootstrapFile,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [switch]$DisposableVM
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if(-not $DisposableVM){throw 'Disposable Windows VM required'}
. "$Scripts\software-update.ps1"
$old=Read-IRVIOInstallation
if((Get-Service IRVIO).Status -ne 'Stopped'){throw 'Stopped disposable desktop required'}
if($old.record.manifest_sha256 -ieq $ManifestSHA256){throw 'Distinct update payload required'}
Read-IRVIOPayload $Payload $ManifestSHA256 $true|Out-Null
New-UpdateDirectory $Evidence
$pipe='\\.\pipe\IRVIO.Control'
$clock=[Diagnostics.Stopwatch]::StartNew()
function API([string]$Command,[string]$Argument=''){
 $ErrorActionPreference='Continue'
 if($Argument){$raw=& $API $Command $pipe $Argument 2> "$Evidence\api-error.txt"}else{$raw=& $API $Command $pipe 2> "$Evidence\api-error.txt"}
 if($LASTEXITCODE){throw "API failed: $Command"}
 return $raw
}
function Network {
 return @(
  Get-NetRoute|ForEach-Object{"route|$($_.InterfaceIndex)|$($_.DestinationPrefix)|$($_.NextHop)|$($_.RouteMetric)|$([uint32]$_.Protocol)"}
  Get-DnsClientServerAddress|ForEach-Object{"dns|$($_.InterfaceIndex)|$($_.AddressFamily)|$($_.ServerAddresses -join ',')"}
  Get-NetAdapter -IncludeHidden|ForEach-Object{"adapter|$($_.InterfaceGuid)"}
 )|Sort-Object
}
$baseline=@(Network)
$baseline|ConvertTo-Json|Set-Content "$Evidence\network-before.json"
Start-Service IRVIO
(Get-Service IRVIO).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
API import-file $BootstrapFile|Out-Null
API private-host|Out-Null
$state=API status|ConvertFrom-Json
if($state.state -ne 'CONNECTION_STATE_HOSTING'){throw 'Private hosting not ready'}
API private-invite-file "$Evidence\private-invitation.txt"|Out-Null
'ready'|Set-Content "$Evidence\host-ready"
$deadline=$clock.ElapsedMilliseconds+300000
do{
 $traffic=API traffic|ConvertFrom-Json
 $traffic|ConvertTo-Json -Compress|Add-Content "$Evidence\host-traffic.ndjson"
 if($traffic.clients -ge 1 -and $traffic.shared_upload_bytes -gt 0 -and (Test-Path "$Evidence\client-probe-ready")){break}
 if($clock.ElapsedMilliseconds -gt $deadline){throw 'Independent downstream user-traffic proof not received'}
 Start-Sleep -Milliseconds 500
}while($true)
$membersBefore=API private-state|ConvertFrom-Json
$clientsBefore=$traffic.clients
if(@($membersBefore.members).Count -lt 1){throw 'Approved membership missing'}
$identity=(Get-FileHash "$($old.root)\profile\identity.key").Hash
@{clients=$traffic.clients;shared_upload_bytes=$traffic.shared_upload_bytes;members=@($membersBefore.members).Count}|ConvertTo-Json|Set-Content "$Evidence\before-update.json"
$start=New-Object Diagnostics.ProcessStartInfo
$start.FileName=$SetupEXE
$start.Arguments='--internal-install-for '+$old.record.desktop_sid+' --development-unattended'
$start.UseShellExecute=$false;$start.CreateNoWindow=$true
$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
$child=[Diagnostics.Process]::Start($start)
$stdout=$child.StandardOutput.ReadToEndAsync();$stderr=$child.StandardError.ReadToEndAsync()
$started=$clock.ElapsedMilliseconds
try{
 if(-not $child.WaitForExit(180000)){throw "Installer PID $($child.Id) still running; preserve and inspect"}
 [IO.File]::WriteAllText("$Evidence\setup.log",$stdout.GetAwaiter().GetResult())
 [IO.File]::WriteAllText("$Evidence\setup-error.log",$stderr.GetAwaiter().GetResult())
 if($child.ExitCode){throw "Embedded EXE failed: $($child.ExitCode)"}
}finally{$child.Dispose()}
@{duration_ms=$clock.ElapsedMilliseconds-$started}|ConvertTo-Json|Set-Content "$Evidence\update-timing.json"
$installed=Read-IRVIOInstallation
if($installed.record.manifest_sha256 -ine $ManifestSHA256 -or (Get-FileHash "$($old.root)\profile\identity.key").Hash -ne $identity){throw 'Updated ownership/identity differs'}
if((Get-Service IRVIO).Status -ne 'Stopped'){throw 'Update revived hosting automatically'}
Stop-IRVIOForMaintenance|Out-Null
try{Assert-IRVIOMaintenanceClean "$Payload\daemon\irviod.exe" (Get-FileHash "$Payload\daemon\irviod.exe").Hash|ConvertTo-Json|Set-Content "$Evidence\native-clean.json"}
finally{Restore-IRVIOMaintenanceStartModes|Out-Null}
$delta=@(Compare-Object $baseline @(Network))
@{delta=$delta}|ConvertTo-Json -Depth 5|Set-Content "$Evidence\network-after.json"
if($delta.Count){throw 'Hosting update left network residue'}
Start-Service IRVIO
(Get-Service IRVIO).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
$idle=API private-summary|ConvertFrom-Json
$traffic=API traffic|ConvertFrom-Json
$membersAfter=API private-state|ConvertFrom-Json
if($idle.state -ne 'CONNECTION_STATE_DISCONNECTED' -or $idle.hosting_wanted -or $traffic.clients -ne 0){throw 'Idle startup retained hosting or downstream clients'}
if(@($membersAfter.members).Count -ne @($membersBefore.members).Count){throw 'Update lost private membership'}
API shutdown|Out-Null
(Get-Service IRVIO).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(60))
@{result='PASS';observed_clients_before=$clientsBefore;downstream_observed_before_update=$true;membership_preserved=$true;idle_after_update=$true;native_cleanup=$true;network_baseline_equal=$true;continuous_packet_capture=$false}|ConvertTo-Json|Set-Content "$Evidence\host-verdict.json"
Get-FileHash $PSCommandPath,$SetupEXE,$API,"$Payload\payload-manifest.json"|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
Get-Content "$Evidence\host-verdict.json"
