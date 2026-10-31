# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([string]$Probe = 'C:\IRVIO-Lab\W03\networkstate-probe.exe', [string]$Output = 'C:\IRVIO-Lab\W03\native-network.json')
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
if (Test-Path $Output) { throw 'Evidence output already exists; choose a new path' }
$adapterName = 'IRVIO-NETSTATE'
if (Get-NetAdapter -Name $adapterName -ErrorAction SilentlyContinue) { throw 'Test adapter already exists; refusing to adopt it' }
$records = [System.Collections.Generic.List[object]]::new()
$process = [System.Diagnostics.Process]::new()
$process.StartInfo = [System.Diagnostics.ProcessStartInfo]::new($Probe)
$process.StartInfo.UseShellExecute = $false
$process.StartInfo.RedirectStandardInput = $true
$process.StartInfo.RedirectStandardOutput = $true
$process.StartInfo.RedirectStandardError = $true
$process.StartInfo.WorkingDirectory = Split-Path -Parent $Probe
if (-not $process.Start()) { throw 'Probe failed to start' }
$stderr = $process.StandardError.ReadToEndAsync()
function Read-Reply {
    $read = $process.StandardOutput.ReadLineAsync()
    if (-not $read.Wait(15000)) { throw 'Probe response timed out' }
    if (-not $read.Result) { throw ('Probe exited: ' + $stderr.Result) }
    return ($read.Result | ConvertFrom-Json)
}
function Get-State {
    $process.StandardInput.WriteLine('state')
    return Read-Reply
}
function Wait-State([string]$label, [scriptblock]$predicate) {
    $deadline = [DateTime]::UtcNow.AddSeconds(12)
    do {
        Start-Sleep -Milliseconds 150
        $state = Get-State
        if (& $predicate $state) {
            $records.Add([pscustomobject]@{step=$label; state=$state})
            return $state
        }
    } while ([DateTime]::UtcNow -lt $deadline)
    $records.Add([pscustomobject]@{step=('failed-' + $label); state=$state; native_ip=(Get-NetIPInterface -InterfaceAlias $adapterName | Select-Object AddressFamily,NlMtu,InterfaceMetric,ConnectionState)})
    throw ('No notification convergence for ' + $label + ': ' + ($state | ConvertTo-Json -Depth 10 -Compress))
}
function Assert-Stable([string]$label, $generation) {
    for ($i=0; $i -lt 6; $i++) {
        Start-Sleep -Milliseconds 150
        $state = Get-State
        if ($state.Generation -ne $generation) { throw ('Unexpected generation at ' + $label) }
    }
    $records.Add([pscustomobject]@{step=$label; state=$state})
    return $state
}
try {
    $ready = Read-Reply
    if ($ready.stage -ne 'ready') { throw 'Missing ready marker' }
    $baseline = $ready.baseline
    $records.Add([pscustomobject]@{step='baseline'; state=$baseline})
    $adapter = Get-NetAdapter -Name $adapterName
    $index = $adapter.ifIndex
    New-NetIPAddress -InterfaceIndex $index -IPAddress 10.253.242.1 -PrefixLength 24 -PolicyStore ActiveStore | Out-Null
    $limit = [DateTime]::UtcNow.AddSeconds(15)
    while ((Get-NetIPAddress -InterfaceIndex $index -IPAddress 10.253.242.1).AddressState -ne 'Preferred') {
        if ([DateTime]::UtcNow -gt $limit) { throw 'Address DAD did not become Preferred' }
        Start-Sleep -Milliseconds 100
    }
    $null = Assert-Stable 'unrelated-adapter-ignored' $baseline.Generation
    Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -AutomaticMetric Disabled -InterfaceMetric 9000 -PolicyStore ActiveStore
    New-NetRoute -InterfaceIndex $index -DestinationPrefix '0.0.0.0/0' -NextHop 10.253.242.254 -RouteMetric 9000 -PolicyStore ActiveStore | Out-Null
    $added = Wait-State 'default-added' { param($s) @($s.Snapshot.Routes | Where-Object { $_.LinkIndex -eq $index -and $_.Priority -eq 18000 }).Count -eq 1 }
    if ($added.Generation -le $baseline.Generation) { throw 'Default addition did not advance generation' }
    Set-NetRoute -InterfaceIndex $index -DestinationPrefix '0.0.0.0/0' -NextHop 10.253.242.254 -RouteMetric 9100 -PolicyStore ActiveStore -Confirm:$false
    $metric = Wait-State 'metric-refreshed' { param($s) @($s.Snapshot.Routes | Where-Object { $_.LinkIndex -eq $index -and $_.Priority -eq 18100 }).Count -eq 1 }
    if ($metric.Generation -ne $added.Generation) { throw 'Metric magnitude-only change advanced generation' }
    $null = Assert-Stable 'metric-no-migration' $metric.Generation
    Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -NlMtuBytes 1400 -PolicyStore ActiveStore
    $records.Add([pscustomobject]@{step='native-mtu-set'; native_ip=(Get-NetIPInterface -InterfaceIndex $index | Select-Object AddressFamily,NlMtu,InterfaceMetric,ConnectionState)})
    $mtu = Wait-State 'mtu-changed' { param($s) @($s.Snapshot.Links | Where-Object { $_.Index -eq $index -and $_.MTU -eq 1400 }).Count -eq 1 }
    if ($mtu.Generation -le $metric.Generation) { throw 'MTU did not advance generation' }
    New-NetIPAddress -InterfaceIndex $index -IPAddress 10.253.242.2 -PrefixLength 24 -PolicyStore ActiveStore | Out-Null
    $address = Wait-State 'address-added' { param($s) @($s.Snapshot.Links | Where-Object { $_.Index -eq $index -and $_.Addresses -contains '10.253.242.2/24' }).Count -eq 1 }
    if ($address.Generation -le $mtu.Generation) { throw 'Address change did not advance generation' }
    Remove-NetRoute -InterfaceIndex $index -DestinationPrefix '0.0.0.0/0' -NextHop 10.253.242.254 -PolicyStore ActiveStore -Confirm:$false
    New-NetRoute -InterfaceIndex $index -DestinationPrefix '0.0.0.0/0' -NextHop 10.253.242.253 -RouteMetric 9100 -PolicyStore ActiveStore | Out-Null
    $gateway = Wait-State 'gateway-changed' { param($s) @($s.Snapshot.Routes | Where-Object { $_.LinkIndex -eq $index -and $_.Gateway -eq '10.253.242.253' }).Count -eq 1 }
    if ($gateway.Generation -le $address.Generation) { throw 'Gateway change did not advance generation' }
    New-NetIPAddress -InterfaceIndex $index -IPAddress 'fd87:7269:6f::1' -PrefixLength 64 -PolicyStore ActiveStore | Out-Null
    Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv6 -AutomaticMetric Disabled -InterfaceMetric 9000 -NlMtuBytes 1500 -PolicyStore ActiveStore
    New-NetRoute -InterfaceIndex $index -DestinationPrefix '::/0' -NextHop 'fd87:7269:6f::fe' -RouteMetric 9000 -PolicyStore ActiveStore | Out-Null
    $v6 = Wait-State 'ipv6-added' { param($s) @($s.Snapshot.Routes | Where-Object { $_.LinkIndex -eq $index -and $_.Family -eq 2 -and $_.MTU -eq 1500 }).Count -eq 1 -and @($s.Snapshot.Links | Where-Object { $_.Index -eq $index -and $_.Addresses -contains 'fd87:7269:6f::1/64' }).Count -eq 1 }
    Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv6 -NlMtuBytes 1450 -PolicyStore ActiveStore
    $v6mtu = Wait-State 'ipv6-mtu-changed' { param($s) @($s.Snapshot.Routes | Where-Object { $_.LinkIndex -eq $index -and $_.Family -eq 2 -and $_.MTU -eq 1450 }).Count -eq 1 }
    if ($v6mtu.Generation -le $v6.Generation) { throw 'IPv6 MTU change above IPv4 MTU did not advance generation' }
    Rename-NetAdapter -Name $adapterName -NewName 'IRVIO-NETSTATE-RENAMED'
    $renamed = Wait-State 'adapter-renamed' { param($s) @($s.Snapshot.Links | Where-Object { $_.Index -eq $index -and $_.Name -eq 'IRVIO-NETSTATE-RENAMED' }).Count -eq 1 }
    if ($renamed.Generation -le $v6mtu.Generation) { throw 'Rename did not advance generation' }
    Rename-NetAdapter -Name 'IRVIO-NETSTATE-RENAMED' -NewName $adapterName
    $null = Wait-State 'adapter-name-restored' { param($s) @($s.Snapshot.Links | Where-Object { $_.Index -eq $index -and $_.Name -eq $adapterName }).Count -eq 1 }
    $process.StandardInput.WriteLine('close-adapter')
    $reply = Read-Reply
    if ($reply.stage -ne 'adapter_closed') { throw 'Adapter close failed' }
    $final = Wait-State 'adapter-removed' { param($s) @($s.Snapshot.Routes | Where-Object { $_.LinkIndex -eq $index }).Count -eq 0 }
    if (($baseline.Snapshot | ConvertTo-Json -Depth 10 -Compress) -ne ($final.Snapshot | ConvertTo-Json -Depth 10 -Compress)) { throw 'Final snapshot differs from baseline' }
    $process.StandardInput.WriteLine('quit')
    if (-not $process.WaitForExit(15000) -or $process.ExitCode -ne 0) { throw ('Probe shutdown failed: ' + $stderr.Result) }
    if (Get-NetAdapter -Name $adapterName -ErrorAction SilentlyContinue) { throw 'Adapter residue' }
    [pscustomobject]@{result='PASS'; records=$records; stderr=$stderr.Result} | ConvertTo-Json -Depth 12 | Set-Content -Encoding UTF8 $Output
    Write-Output ('PASS: ' + $Output)
} finally {
    if (-not $process.HasExited) {
        $process.StandardInput.WriteLine('quit')
        if (-not $process.WaitForExit(15000)) { $process.Kill(); $process.WaitForExit() }
    }
    if (-not (Test-Path $Output)) {
        [pscustomobject]@{result='FAIL'; records=$records; stderr=$stderr.Result} | ConvertTo-Json -Depth 12 | Set-Content -Encoding UTF8 $Output
    }
    $process.Dispose()
}
