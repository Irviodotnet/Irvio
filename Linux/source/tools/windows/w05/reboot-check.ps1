# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([switch]$AfterReboot,[string]$Evidence='C:\IRVIO-Lab\evidence\w05-filter-reboot')
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$exe='C:\IRVIO-Lab\W05\filterprobe.exe'
function Test-TCP {
    $c=[Net.Sockets.TcpClient]::new()
    try{return ($c.ConnectAsync('192.168.23.2',49494).Wait(1500) -and $c.Connected)}catch{return $false}finally{$c.Dispose()}
}
function Test-UDP {
    $c=[Net.Sockets.UdpClient]::new()
    try{
        $c.Client.ReceiveTimeout=1000;$c.Connect('192.168.23.2',49495)
        $token='irvio-nat-wfp-reboot-'+[Guid]::NewGuid().ToString('N')
        $bytes=[Text.Encoding]::ASCII.GetBytes($token);$c.Send($bytes,$bytes.Length) | Out-Null
        $from=[Net.IPEndPoint]::new([Net.IPAddress]::Any,0)
        return ([Text.Encoding]::ASCII.GetString($c.Receive([ref]$from)) -eq $token)
    }catch{return $false}finally{$c.Dispose()}
}
$boot=(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')
if($AfterReboot){
    $before=Get-Content -Raw ($Evidence+'\before.json') | ConvertFrom-Json
    if($before.boot -eq $boot){throw 'No actual reboot occurred'}
    $failure=$null;$snapshot=$null;$blocked=$false;$restored=$false
    try{
        $snapshot=& $exe inspect | ConvertFrom-Json
        if($LASTEXITCODE -or -not $snapshot.Active -or $snapshot.Rules.Count -ne 4){throw 'Persistent policy not active after reboot'}
        $blocked=(-not (Test-TCP)) -and (-not (Test-UDP))
        if(-not $blocked){throw 'Traffic escaped after reboot'}
    }catch{$failure=$_.Exception.Message;throw}
    finally{
        & $exe remove
        if($LASTEXITCODE){throw 'Post-reboot policy cleanup failed'}
        $end=& $exe inspect | ConvertFrom-Json
        if($LASTEXITCODE -or $end.Installed){throw 'Post-reboot policy residue'}
        $restored=(Test-TCP) -and (Test-UDP)
        [pscustomobject]@{boot=$boot;previous_boot=$before.boot;blocked=$blocked;restored=$restored;policy=$snapshot;final=$end;failure=$failure;secure_boot=(Confirm-SecureBootUEFI);defender=(Get-MpComputerStatus | Select-Object AntivirusEnabled,RealTimeProtectionEnabled)} | ConvertTo-Json -Depth 8 | Set-Content -Encoding UTF8 ($Evidence+'\after.json')
        if(-not $restored){throw 'Oracle not restored after cleanup'}
    }
    Write-Output 'W05 persistent policy across real VM reboot PASS'
    exit 0
}
if(Test-Path $Evidence){throw 'Evidence already exists'}
$state=& $exe inspect | ConvertFrom-Json
if($LASTEXITCODE -or $state.Installed){throw 'Existing policy fixture'}
if(-not (Test-TCP) -or -not (Test-UDP)){throw 'Oracle unavailable before reboot test'}
New-Item -ItemType Directory $Evidence | Out-Null
$p=$null;$readyForBoot=$false
try{
    $start=[Diagnostics.ProcessStartInfo]::new($exe,'install-hold')
    $start.UseShellExecute=$false;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $p=[Diagnostics.Process]::Start($start);$stderr=$p.StandardError.ReadToEndAsync()
    $ready=$p.StandardOutput.ReadLineAsync()
    if(-not $ready.Wait(15000) -or -not $ready.Result){throw 'No persistent policy readiness'}
    $snapshot=$ready.Result | ConvertFrom-Json
    if(-not $snapshot.Active -or $snapshot.Rules.Count -ne 4){throw 'Incomplete policy'}
    $p.Kill();$p.WaitForExit()
    if((Test-TCP) -or (Test-UDP)){throw 'Post-kill protection failed before reboot'}
    [pscustomobject]@{boot=$boot;policy=$snapshot;service=(Get-Service IRVIO-W05-Owner | Select-Object Name,Status,StartType)} | ConvertTo-Json -Depth 8 | Set-Content -Encoding UTF8 ($Evidence+'\before.json')
    $readyForBoot=$true
}finally{
    if($p){if(-not $p.HasExited){$p.Kill();$p.WaitForExit()};$stderr.Result | Set-Content ($Evidence+'\probe-stderr.txt');$p.Dispose()}
    if(-not $readyForBoot){& $exe remove;if($LASTEXITCODE){throw 'Pre-reboot cleanup failed'}}
}
shutdown.exe /r /t 5 /f
if($LASTEXITCODE){& $exe remove;throw 'Could not schedule VM reboot'}
Write-Output 'VM reboot scheduled with scoped persistent oracle policy'
