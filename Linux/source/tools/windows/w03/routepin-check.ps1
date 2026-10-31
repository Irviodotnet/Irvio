# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([string]$Probe='C:\IRVIO-Lab\W03-route\routepin-probe.exe', [string]$Output='C:\IRVIO-Lab\W03-route\routepin.json')
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if (Test-Path $Output) { throw 'Evidence already exists' }
if (Get-NetAdapter -Name IRVIO-ROUTE -ErrorAction SilentlyContinue) { throw 'Owned adapter already exists' }
$records=[System.Collections.Generic.List[object]]::new()
$p=[System.Diagnostics.Process]::new()
$p.StartInfo=[System.Diagnostics.ProcessStartInfo]::new($Probe,'192.168.23.2')
$p.StartInfo.UseShellExecute=$false
$p.StartInfo.RedirectStandardInput=$true
$p.StartInfo.RedirectStandardOutput=$true
$p.StartInfo.RedirectStandardError=$true
if (-not $p.Start()) { throw 'Probe launch failed' }
$stderr=$p.StandardError.ReadToEndAsync()
$physical=$null
$disabled=$false
$pass=$false
function Reply {
    $task=$p.StandardOutput.ReadLineAsync()
    if (-not $task.Wait(10000)) { throw 'Probe timed out' }
    if (-not $task.Result) { throw ('Probe exited: '+$stderr.Result) }
    return ($task.Result | ConvertFrom-Json)
}
function Request([string]$command,[bool]$expected) {
    $p.StandardInput.WriteLine($command)
    $reply=Reply
    $records.Add($reply)
    if ($reply.success -ne $expected) { throw ('Unexpected '+$command+': '+($reply | ConvertTo-Json -Depth 8 -Compress)) }
    if ($command -in @('physical','physical-udp','held-udp','held-mux','new-mux') -and $reply.tun_packets -ne 0) { throw 'Pinned socket fell back to TUN' }
    return $reply
}
try {
    $ready=Reply
    if ($ready.stage -ne 'ready') { throw 'No baseline' }
    $records.Add($ready)
    $physical=Get-NetAdapter | Where-Object { $_.ifIndex -eq $ready.baseline.Binding.Index }
    if (@($physical).Count -ne 1 -or $physical.Status -ne 'Up' -or $physical.Name -eq 'IRVIO-ROUTE') { throw 'Physical NIC is ambiguous' }
    $tun=Get-NetAdapter -Name IRVIO-ROUTE
    New-NetIPAddress -InterfaceIndex $tun.ifIndex -IPAddress 10.253.243.1 -PrefixLength 24 -PolicyStore ActiveStore | Out-Null
    $deadline=[DateTime]::UtcNow.AddSeconds(20)
    while ((Get-NetIPAddress -InterfaceIndex $tun.ifIndex -IPAddress 10.253.243.1).AddressState -ne 'Preferred') {
        if ([DateTime]::UtcNow -gt $deadline) { throw 'DAD timeout' }
        Start-Sleep -Milliseconds 200
    }
    $null=Request 'ordinary' $true
    $null=Request 'ordinary-udp' $true
    $null=Request 'physical-udp' $true
    $null=Request 'held-udp' $true
    $null=Request 'held-mux' $true
    $null=Request 'new-mux' $true
    New-NetRoute -InterfaceIndex $tun.ifIndex -DestinationPrefix '192.168.23.2/32' -NextHop 0.0.0.0 -RouteMetric 1 -PolicyStore ActiveStore | Out-Null
    $kernel=Request 'kernel' $true
    if ($kernel.path.Binding.LUID -ne $ready.tun_luid) { throw 'Kernel did not select the more-specific tunnel route' }
    $null=Request 'exclude-tun' $false
    $constrained=Request 'constrained' $true
    if ($constrained.path.Binding.LUID -ne $ready.baseline.Binding.LUID) { throw 'Constrained route changed interface' }
    $null=Request 'ordinary' $false
    $null=Request 'physical' $true
    $captured=Request 'ordinary-udp' $false
    if ($captured.tun_packets -lt 1) { throw 'Negative control did not exercise the TUN path' }
    $null=Request 'physical-udp' $true
    $null=Request 'held-udp' $true
    $null=Request 'held-mux' $true
    $null=Request 'new-mux' $true
    $disabled=$true
    $physical | Disable-NetAdapter -Confirm:$false
    $null=Request 'physical' $false
    $null=Request 'physical-udp' $false
    $null=Request 'held-udp' $false
    $null=Request 'held-mux' $false
    $null=Request 'new-mux' $false
    $null=Request 'constrained' $false
    Get-NetAdapter -Name $physical.Name | Enable-NetAdapter -Confirm:$false
    $disabled=$false
    $deadline=[DateTime]::UtcNow.AddSeconds(30)
    do {
        Start-Sleep -Milliseconds 500
        $p.StandardInput.WriteLine('physical')
        $recovered=Reply
        $records.Add($recovered)
    } while (-not $recovered.success -and [DateTime]::UtcNow -lt $deadline)
    if (-not $recovered.success) { throw 'Physical path did not recover' }
    $null=Request 'physical-udp' $true
    $null=Request 'held-udp' $true
    $null=Request 'held-mux' $true
    $null=Request 'new-mux' $true
    $null=Request 'close-adapter' $true
    $null=Request 'ordinary' $true
    $null=Request 'ordinary-udp' $true
    $p.StandardInput.WriteLine('quit')
    if (-not $p.WaitForExit(10000) -or $p.ExitCode -ne 0) { throw 'Probe did not exit cleanly' }
    if (Get-NetAdapter -Name IRVIO-ROUTE -ErrorAction SilentlyContinue) { throw 'Adapter residue' }
    if (Get-NetRoute -DestinationPrefix '192.168.23.2/32' -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceIndex -eq $tun.ifIndex }) { throw 'Route residue' }
    $pass=$true
} finally {
    if ($disabled -and $physical) { Get-NetAdapter -Name $physical.Name | Enable-NetAdapter -Confirm:$false }
    if (-not $p.HasExited) {
        $p.StandardInput.WriteLine('quit')
        if (-not $p.WaitForExit(10000)) { $p.Kill(); $p.WaitForExit() }
    }
    [pscustomobject]@{result=$(if($pass){'PASS'}else{'FAIL'}); records=$records; stderr=$stderr.Result} | ConvertTo-Json -Depth 12 | Set-Content -Encoding UTF8 $Output
    $p.Dispose()
}
Write-Output ('PASS: '+$Output)
