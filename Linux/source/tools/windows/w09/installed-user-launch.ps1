# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Evidence,[switch]$DisposableVM)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
Set-StrictMode -Version Latest
if(-not $DisposableVM){throw 'Disposable VM required'}
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
$principal=New-Object Security.Principal.WindowsPrincipal($identity)
if($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Non-elevated interactive token required'}
if([Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0){throw 'Interactive desktop required'}
if(Test-Path $Evidence){throw 'New evidence directory required'}
if(@(Get-Process irvio,irvio-ui-bridge -ErrorAction SilentlyContinue).Count){throw 'Existing user UI/helper must be preserved'}
New-Item -ItemType Directory $Evidence|Out-Null
$ui=Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'IRVIO\ui'
$shortcut=Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'IRVIO.lnk'
$shell=New-Object -ComObject WScript.Shell
try{
 $link=$shell.CreateShortcut($shortcut)
 if($link.TargetPath -ine (Join-Path $ui 'irvio.exe') -or $link.WorkingDirectory -ine $ui){throw 'Installed shortcut target differs'}
}finally{if($link){[void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($link)};[void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($shell)}
$process=$null;$stdout=$null;$stderr=$null
try{
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName=Join-Path $ui 'irvio.exe';$start.WorkingDirectory=$ui
 $start.UseShellExecute=$false;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $start.EnvironmentVariables['PATH']="$env:SystemRoot\System32;$env:SystemRoot"
 $process=[Diagnostics.Process]::Start($start)
 $stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
 $clock=[Diagnostics.Stopwatch]::StartNew()
 do{
  if($process.HasExited){throw "Installed UI exited: $($process.ExitCode)"}
  $process.Refresh()
  if($process.MainWindowHandle -ne [IntPtr]::Zero){break}
  if($clock.Elapsed.TotalSeconds -gt 30){throw 'No interactive window within deadline'}
  Start-Sleep -Milliseconds 200
 }while($true)
 $modules=@($process.Modules|Select-Object ModuleName,FileName)
 foreach($name in @('msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll','flutter_windows.dll')){
  $found=@($modules|Where-Object ModuleName -ieq $name)
  if($found.Count -ne 1 -or $found[0].FileName -ine (Join-Path $ui $name)){throw "Runtime not loaded from installation: $name"}
 }
 $modules|ConvertTo-Json|Set-Content "$Evidence\modules.json"
 @{elevated=$false;session_id=$process.SessionId;ui_pid=$process.Id;window_created=$true;shortcut_verified=$true;window_ms=$clock.ElapsedMilliseconds}|ConvertTo-Json|Set-Content "$Evidence\ui-ready.json"
 do{
  if($process.HasExited){throw 'UI died while coordinator checked daemon'}
  if(Test-Path "$Evidence\coordinator-release"){break}
  if($clock.Elapsed.TotalSeconds -gt 120){throw 'Coordinator did not finish within deadline'}
  Start-Sleep -Milliseconds 250
 }while($true)
 Start-Sleep -Seconds 10
 if($process.HasExited){throw 'UI exited before delayed visual capture'}
 Add-Type -AssemblyName System.Windows.Forms,System.Drawing
 $bounds=[Windows.Forms.Screen]::PrimaryScreen.Bounds
 $bitmap=New-Object Drawing.Bitmap($bounds.Width,$bounds.Height)
 $graphics=[Drawing.Graphics]::FromImage($bitmap)
 try{$graphics.CopyFromScreen($bounds.Location,[Drawing.Point]::Empty,$bounds.Size);$bitmap.Save("$Evidence\desktop.png",[Drawing.Imaging.ImageFormat]::Png)}finally{$graphics.Dispose();$bitmap.Dispose()}
 @{capture_elapsed_ms=$clock.ElapsedMilliseconds;visual_acceptance='requires separate inspection'}|ConvertTo-Json|Set-Content "$Evidence\capture.json"
}finally{
 if($null -ne $process){
  if(-not $process.HasExited){$process.Kill();if(-not $process.WaitForExit(10000)){throw 'Owned UI did not stop'}}
  if($null -ne $stdout){[IO.File]::WriteAllText("$Evidence\ui-stdout.log",$stdout.GetAwaiter().GetResult())}
  if($null -ne $stderr){[IO.File]::WriteAllText("$Evidence\ui-stderr.log",$stderr.GetAwaiter().GetResult())}
  $process.Dispose()
 }
}
$clock=[Diagnostics.Stopwatch]::StartNew()
while(@(Get-Process irvio,irvio-ui-bridge -ErrorAction SilentlyContinue).Count){
 if($clock.Elapsed.TotalSeconds -gt 10){throw 'UI/helper residue'}
 Start-Sleep -Milliseconds 200
}
@{result='PASS';elevated=$false;installed_ui_loaded=$true;ui_helper_gone=$true;scope='ordinary-token installed UI and app-local modules; daemon/API/cleanup checked independently'}|ConvertTo-Json|Set-Content "$Evidence\user-verdict.json"
