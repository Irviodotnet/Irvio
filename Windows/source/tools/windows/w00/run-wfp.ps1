# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$root = 'C:\IRVIO-Lab\W00'
$exe = "$root\wfp-lifetime.exe"
function Test-Oracle {
    $client = [Net.Sockets.TcpClient]::new()
    try {
        $task = $client.ConnectAsync('192.168.23.2',49494)
        return ($task.Wait(3000) -and $client.Connected)
    } catch { return $false }
    finally { $client.Dispose() }
}
if (-not (Test-Oracle)) { throw 'Independent oracle unavailable before WFP test' }
$results = @()
foreach ($mode in @('dynamic','static','persistent')) {
    $process = $null
    try {
        $start = [Diagnostics.ProcessStartInfo]::new($exe,$mode)
        $start.UseShellExecute = $false
        $start.RedirectStandardOutput = $true
        $process = [Diagnostics.Process]::Start($start)
        $ready = $process.StandardOutput.ReadLineAsync()
        if (-not $ready.Wait(15000)) { throw 'Filter creation timed out' }
        if (-not $ready.Result -or ($ready.Result | ConvertFrom-Json).stage -ne 'filter_ready') { throw 'No filter_ready record' }
        $beforeKill = Test-Oracle
        $process.Kill()
        $process.WaitForExit()
        Start-Sleep -Milliseconds 500
        $afterKill = Test-Oracle
        & $exe remove | Out-Null
        if ($LASTEXITCODE) { throw 'Owned WFP filter cleanup failed' }
        $afterCleanup = Test-Oracle
        $pass = (-not $beforeKill) -and $afterCleanup -and ($afterKill -eq ($mode -eq 'dynamic'))
        $results += [ordered]@{ mode=$mode; during_filter=$beforeKill; after_process_kill=$afterKill; after_cleanup=$afterCleanup; semantics_match=$pass }
        if (-not $pass) { throw "Unexpected WFP lifetime behavior: $mode" }
    } finally {
        if ($process) { if (-not $process.HasExited) {$process.Kill(); $process.WaitForExit()}; $process.Dispose() }
        & $exe remove | Out-Null
        $results | ConvertTo-Json -Depth 5 | Set-Content "$root\wfp-lifetime-result.json"
    }
}
$results | ConvertTo-Json -Depth 5
