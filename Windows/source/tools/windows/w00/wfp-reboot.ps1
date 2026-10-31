# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][ValidateSet('Prepare','Verify')][string]$Stage)
$ErrorActionPreference='Stop'
$root='C:\IRVIO-Lab\W00'
$exe="$root\wfp-lifetime.exe"
function Test-Oracle {
    $client=[Net.Sockets.TcpClient]::new()
    try { $task=$client.ConnectAsync('192.168.23.2',49494); return ($task.Wait(3000) -and $client.Connected) }
    catch { return $false }
    finally { $client.Dispose() }
}
$boot=(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o')
if ($Stage -eq 'Prepare') {
    if (-not (Test-Oracle)) { throw 'Oracle must work before test' }
    $process=$null
    try {
        $start=[Diagnostics.ProcessStartInfo]::new($exe,'persistent')
        $start.UseShellExecute=$false
        $start.RedirectStandardOutput=$true
        $process=[Diagnostics.Process]::Start($start)
        $ready=$process.StandardOutput.ReadLineAsync()
        if (-not $ready.Wait(15000) -or -not $ready.Result -or ($ready.Result | ConvertFrom-Json).stage -ne 'filter_ready') { throw 'Filter readiness failed' }
        $ready.Result | Set-Content "$root\wfp-before-reboot.json"
        $boot | Set-Content "$root\previous-boot.txt"
    } catch {
        & $exe remove | Out-Null
        throw
    } finally {
        if ($process) { if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() }; $process.Dispose() }
    }
    if (Test-Oracle) { throw 'Persistent filter did not block oracle after kill' }
    Write-Output 'Prepared. Reboot only the disposable VM, then run -Stage Verify.'
    exit 0
}
$previous=(Get-Content "$root\previous-boot.txt").Trim()
if ($boot -eq $previous) { throw 'A new OS boot is required' }
$blocked= -not (Test-Oracle)
& $exe remove | Out-Null
if ($LASTEXITCODE) { throw 'Owned filter cleanup failed' }
$restored=Test-Oracle
[ordered]@{previous_boot=$previous; current_boot=$boot; oracle_blocked_before_cleanup=$blocked; oracle_reachable_after_cleanup=$restored} | ConvertTo-Json | Tee-Object "$root\wfp-reboot-result.json"
if (-not $blocked -or -not $restored) { throw 'Persistent WFP reboot test failed' }
