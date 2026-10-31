# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$root = 'C:\IRVIO-Lab\W00'
$rule = 'IRVIO-W00-Wintun'
$exe = "$root\wintun-probe.exe"
New-Item -ItemType Directory -Force $root | Out-Null
if (Get-NetAdapter -Name IRVIO-W00 -IncludeHidden -ErrorAction SilentlyContinue) { throw 'Probe adapter already exists' }
if (Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue) { throw 'Probe rule already exists' }
$sig = Get-AuthenticodeSignature "$root\wintun.dll"
if ($sig.Status -ne 'Valid') { throw 'Wintun signature invalid' }
$sig | Select-Object Status,@{n='Signer';e={$_.SignerCertificate.Subject}} | ConvertTo-Json | Set-Content "$root\wintun-signature.json"
$start = [Diagnostics.ProcessStartInfo]::new($exe)
$start.UseShellExecute = $false
$start.RedirectStandardInput = $true
$start.RedirectStandardOutput = $true
$start.RedirectStandardError = $true
$process = [Diagnostics.Process]::new()
$process.StartInfo = $start
$result = [ordered]@{packet_io='NOT RUN'; native_nat='NOT RUN'; cleanup='NOT RUN'}
$createdRule = $false
$createdNat = $false
$started = $false
try {
    if (-not $process.Start()) { throw 'Probe process failed to start' }
    $started = $true
    $errors = $process.StandardError.ReadToEndAsync()
    $readyTask = $process.StandardOutput.ReadLineAsync()
    if (-not $readyTask.Wait(45000)) { throw 'Probe startup timeout' }
    $ready = $readyTask.Result
    if (-not $ready) { throw 'Probe exited before adapter_ready' }
    $ready | Set-Content "$root\wintun-ready.json"
    if (($ready | ConvertFrom-Json).stage -ne 'adapter_ready') { throw 'Unexpected probe startup output' }
    $adapter = Get-NetAdapter -Name IRVIO-W00 -IncludeHidden
    New-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress 10.253.240.1 -PrefixLength 24 -AddressFamily IPv4 | Out-Null
    Set-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -NlMtuBytes 1280
    New-NetFirewallRule -Name $rule -DisplayName $rule -Direction Inbound -Action Allow -Protocol UDP -LocalPort 49001 -LocalAddress 10.253.240.1 -RemoteAddress 10.253.240.2 -Program $exe -Profile Any | Out-Null
    $createdRule = $true
    try {
        New-NetNat -Name IRVIO-W00 -InternalIPInterfaceAddressPrefix 10.253.240.0/24 | Out-Null
        $createdNat = $true
        $result.native_nat = 'CREATED; FORWARDING NOT YET TESTED'
    } catch { $result.native_nat = $_.Exception.Message }
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        $address = Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -IPAddress 10.253.240.1
        if ($address.AddressState -eq 'Preferred') { break }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    [ordered]@{adapter=($adapter | Select-Object Name,ifIndex,Status); address=($address | Select-Object IPAddress,AddressState,PolicyStore)} | ConvertTo-Json -Depth 4 | Set-Content "$root\wintun-ip.json"
    if ($address.AddressState -ne 'Preferred') { throw "Probe address is not ready: $($address.AddressState)" }
    $process.StandardInput.WriteLine('ready')
    $process.StandardInput.Flush()
    if (-not $process.WaitForExit(20000)) { throw 'Packet I/O probe timeout' }
    $result.packet_io = $process.StandardOutput.ReadToEnd().Trim()
    $errors.Result | Set-Content "$root\wintun-stderr.log"
    if ($process.ExitCode -ne 0) { throw "Wintun probe exit $($process.ExitCode): $($errors.Result)" }
    if (($result.packet_io | ConvertFrom-Json).result -ne 'PASS') { throw 'No packet I/O PASS record' }
} finally {
    if ($started -and -not $process.HasExited) { $process.Kill(); $process.WaitForExit() }
    if ($createdNat) { Remove-NetNat -Name IRVIO-W00 -Confirm:$false }
    if ($createdRule) { Remove-NetFirewallRule -Name $rule }
    $left = @(Get-NetAdapter -Name IRVIO-W00 -IncludeHidden -ErrorAction SilentlyContinue)
    $result.cleanup = if ($left.Count -eq 0) {'PASS'} else {'ADAPTER RESIDUE'}
    $result | ConvertTo-Json -Depth 4 | Tee-Object "$root\wintun-result.json"
    $process.Dispose()
}
if ($result.cleanup -ne 'PASS') { throw 'Probe cleanup incomplete' }
