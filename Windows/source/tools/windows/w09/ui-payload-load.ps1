# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [switch]$DisposableVM
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if(-not $DisposableVM){throw 'Disposable Windows VM required'}
. "$PSScriptRoot\..\..\..\packaging\windows\payload.ps1"
$manifest=Read-IRVIOPayload $Payload $ManifestSHA256 $true
if(Test-Path -LiteralPath $Evidence){throw 'Evidence already exists'}
if(@(Get-Process irvio,irvio-ui-bridge,irviod -ErrorAction SilentlyContinue).Count){throw 'Idle VM with no IRVIO processes required'}
$services=@(Get-CimInstance Win32_Service -Filter "Name='IRVIO' OR Name='IRVIO-Recovery'" | Select-Object Name,State,StartMode,ProcessId)
$main=@($services | Where-Object Name -eq IRVIO)
if($main.Count -ne 1 -or $main[0].State -ne 'Stopped' -or $main[0].StartMode -ne 'Disabled'){throw 'Disabled stopped disposable service required; this gate must not activate networking'}
New-Item -ItemType Directory -Path $Evidence | Out-Null
$services | ConvertTo-Json | Set-Content "$Evidence\services-before.json"
$manifestHash=(Get-FileHash "$Payload\payload-manifest.json").Hash
$ui=Join-Path $Evidence 'ui'
New-Item -ItemType Directory -Path $ui | Out-Null
foreach($file in $manifest.files){
 if(-not $file.path.StartsWith('ui/')){continue}
 $destination=Join-Path $Evidence $file.path
 [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($destination)) | Out-Null
 Copy-Item -LiteralPath (Join-Path $Payload $file.path) -Destination $destination
 if((Get-FileHash $destination).Hash -ine $file.sha256){throw 'UI copy hash mismatch'}
}
$process=$null
$stdout=$null
$stderr=$null
try{
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName=Join-Path $ui 'irvio.exe'
 $start.WorkingDirectory=$ui
 $start.UseShellExecute=$false
 $start.RedirectStandardOutput=$true
 $start.RedirectStandardError=$true
 $start.EnvironmentVariables['PATH']="$env:SystemRoot\System32;$env:SystemRoot"
 $process=[Diagnostics.Process]::Start($start)
 $stdout=$process.StandardOutput.ReadToEndAsync()
 $stderr=$process.StandardError.ReadToEndAsync()
 $watch=[Diagnostics.Stopwatch]::StartNew()
 $required=@('msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll','flutter_windows.dll')
 $modules=@()
 do{
  if($process.HasExited){throw "UI exited during loader check: $($process.ExitCode)"}
  $process.Refresh()
  $modules=@($process.Modules | Select-Object ModuleName,FileName)
  $missing=@($required | Where-Object {$_ -notin $modules.ModuleName})
  if(-not $missing.Count -and ($process.SessionId -eq 0 -or $process.MainWindowHandle -ne [IntPtr]::Zero)){break}
  Start-Sleep -Milliseconds 200
 }while($watch.Elapsed.TotalSeconds -lt 30)
 $modules | ConvertTo-Json | Set-Content "$Evidence\modules.json"
 if($missing.Count){throw "Required UI modules not loaded: $($missing -join ',')"}
 if($process.SessionId -ne 0 -and $process.MainWindowHandle -eq [IntPtr]::Zero){throw 'Interactive UI did not create a window'}
 foreach($name in $required){
  $loaded=@($modules | Where-Object ModuleName -ieq $name)
  if($loaded.Count -ne 1 -or $loaded[0].FileName -ine (Join-Path $ui $name)){throw "Module not loaded app-locally: $name"}
 }
 Start-Sleep -Seconds 3
 if($process.HasExited){throw 'UI exited after loading dependencies'}
 $principal=New-Object Security.Principal.WindowsPrincipal ([Security.Principal.WindowsIdentity]::GetCurrent())
 @{result='PASS';scope='app-local runtime loading and window creation where interactive; not full UX or clean-host installation';manifest_sha256=$manifestHash;loaded_from_payload=$required;isolated_path=$start.EnvironmentVariables['PATH'];session_id=$process.SessionId;window_created=($process.MainWindowHandle -ne [IntPtr]::Zero);elevated=$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator);load_ms=$watch.ElapsedMilliseconds} | ConvertTo-Json | Set-Content "$Evidence\loader-result.json"
}finally{
 if($null -ne $process){
  if(-not $process.HasExited){$process.Kill();if(-not $process.WaitForExit(10000)){throw 'Owned UI process did not exit'}}
  if($null -ne $stdout){[IO.File]::WriteAllText("$Evidence\ui-stdout.log",$stdout.GetAwaiter().GetResult())}
  if($null -ne $stderr){[IO.File]::WriteAllText("$Evidence\ui-stderr.log",$stderr.GetAwaiter().GetResult())}
  $process.Dispose()
 }
}
$watch=[Diagnostics.Stopwatch]::StartNew()
do{
 $left=@(Get-Process irvio,irvio-ui-bridge,irviod -ErrorAction SilentlyContinue)
 if(-not $left.Count){break}
 Start-Sleep -Milliseconds 200
}while($watch.Elapsed.TotalSeconds -lt 10)
if($left.Count){throw 'UI/helper/daemon process remains; preserved for inspection'}
$after=@(Get-CimInstance Win32_Service -Filter "Name='IRVIO' OR Name='IRVIO-Recovery'" | Select-Object Name,State,StartMode,ProcessId)
$after | ConvertTo-Json | Set-Content "$Evidence\services-after.json"
if(Compare-Object $services $after -Property Name,State,StartMode,ProcessId){throw 'Service state changed'}
@{result='PASS';services_unchanged=$true;irvio_processes=0;manifest_sha256=$manifestHash} | ConvertTo-Json | Set-Content "$Evidence\verdict.json"
Get-Content "$Evidence\loader-result.json","$Evidence\verdict.json"
