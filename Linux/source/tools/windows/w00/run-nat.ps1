# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$root='C:\IRVIO-Lab\W00'
$name='IRVIO-W00'
$rule='IRVIO-W00-NAT'
$prefix='10.253.240.0/24'
if (@(Get-NetNat).Count) { throw 'Existing host NAT: refusing feasibility probe' }
if (Get-NetAdapter -Name $name -IncludeHidden -ErrorAction SilentlyContinue) { throw 'Probe adapter already exists' }
if (Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue) { throw 'Probe firewall rule already exists' }
if (Get-NetRoute -DestinationPrefix $prefix -ErrorAction SilentlyContinue) { throw 'Lab prefix route already exists' }
if ((Get-AuthenticodeSignature "$root\wintun.dll").Status -ne 'Valid') { throw 'Invalid Wintun signature' }
$process=$null
$createdNat=$false
$createdRule=$false
$physical=$null
$result=[ordered]@{packet_io='NOT RUN'; cleanup='NOT RUN'}
try {
    $start=[Diagnostics.ProcessStartInfo]::new("$root\wintun-probe.exe",'nat')
    $start.UseShellExecute=$false
    $start.RedirectStandardInput=$true
    $start.RedirectStandardOutput=$true
    $start.RedirectStandardError=$true
    $process=[Diagnostics.Process]::Start($start)
    $errors=$process.StandardError.ReadToEndAsync()
    $ready=$process.StandardOutput.ReadLineAsync()
    if (-not $ready.Wait(45000) -or -not $ready.Result -or ($ready.Result | ConvertFrom-Json).stage -ne 'adapter_ready') { throw 'Adapter readiness failed' }
    $adapter=Get-NetAdapter -Name $name -IncludeHidden
    New-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress 10.253.240.1 -PrefixLength 24 | Out-Null
    Set-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -NlMtuBytes 1280 -Forwarding Enabled -PolicyStore ActiveStore
    $route=Find-NetRoute -RemoteIPAddress 192.168.23.2
    $indices=@($route.InterfaceIndex | Sort-Object -Unique)
    if ($indices.Count -ne 1 -or $indices[0] -eq $adapter.ifIndex) { throw 'Ambiguous physical route' }
    $physical=Get-NetIPInterface -InterfaceIndex $indices[0] -AddressFamily IPv4 -PolicyStore ActiveStore
    Set-NetIPInterface -InterfaceIndex $physical.InterfaceIndex -AddressFamily IPv4 -Forwarding Enabled -PolicyStore ActiveStore
    New-NetNat -Name $name -InternalIPInterfaceAddressPrefix $prefix | Out-Null
    $createdNat=$true
    New-NetFirewallRule -Name $rule -DisplayName $rule -Direction Inbound -Action Allow -Protocol UDP -LocalAddress 192.168.23.2 -RemoteAddress $prefix -LocalPort 49495 -Profile Any | Out-Null
    $createdRule=$true
    $deadline=[DateTime]::UtcNow.AddSeconds(15)
    do {
        $address=Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress 10.253.240.1
        if ($address.AddressState -eq 'Preferred') { break }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    if ($address.AddressState -ne 'Preferred') { throw 'Probe address not ready' }
    $process.StandardInput.WriteLine('ready')
    $process.StandardInput.Flush()
    if (-not $process.WaitForExit(25000)) { throw 'NAT probe timeout' }
    $result.packet_io=$process.StandardOutput.ReadToEnd().Trim()
    $errors.Result | Set-Content "$root\nat-probe-stderr.log"
    Get-NetNatSession | Select-Object Protocol,InternalSourceAddress,InternalSourcePort,ExternalSourceAddress,ExternalSourcePort,ExternalDestinationAddress,ExternalDestinationPort | ConvertTo-Json | Set-Content "$root\nat-probe-sessions.json"
    if ($process.ExitCode -ne 0) { throw "NAT probe failed: $($errors.Result)" }
    if (($result.packet_io | ConvertFrom-Json).result -ne 'PASS') { throw 'No NAT PASS record' }
} finally {
    if ($process) { if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() }; $process.Dispose() }
    if ($createdRule) { Remove-NetFirewallRule -Name $rule }
    if ($createdNat) { Remove-NetNat -Name $name -Confirm:$false }
    if ($physical) { Set-NetIPInterface -InterfaceIndex $physical.InterfaceIndex -AddressFamily IPv4 -Forwarding $physical.Forwarding -PolicyStore ActiveStore }
    $residue=@(Get-NetAdapter -Name $name -IncludeHidden -ErrorAction SilentlyContinue)
    $natResidue=@(Get-NetNat | Where-Object Name -eq $name)
    $ruleResidue=@(Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue)
    $forwardingRestored=$true
    if ($physical) { $forwardingRestored=(Get-NetIPInterface -InterfaceIndex $physical.InterfaceIndex -AddressFamily IPv4 -PolicyStore ActiveStore).Forwarding -eq $physical.Forwarding }
    $result.cleanup=if ($residue.Count -or $natResidue.Count -or $ruleResidue.Count -or -not $forwardingRestored) {'RESIDUE'} else {'PASS'}
    $result | ConvertTo-Json -Depth 4 | Tee-Object "$root\nat-probe-result.json"
}
if ($result.cleanup -ne 'PASS') { throw 'NAT probe cleanup incomplete' }
