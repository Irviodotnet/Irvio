# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(Test-Path $Evidence){throw 'Evidence directory already exists'}
New-Item -ItemType Directory $Evidence | Out-Null
$exe='C:\IRVIO-Lab\W05\protected.test.exe'
function Probe([string]$stage,[bool]$want){
    $env:IRVIO_WINDOWS_WAN_HELPER='1';$env:IRVIO_WINDOWS_WAN_STAGE=$stage
    $lines=& C:\IRVIO-Lab\W05\foreign-protected.test.exe '-test.run=^TestWindowsWANProbeHelper$'
    if($LASTEXITCODE){throw 'Probe subprocess failed'}
    $line=@($lines | Where-Object {$_ -like 'WAN_RESULT *'})
    if($line.Count -ne 1){throw 'Probe result missing'}
    $r=$line[0].Substring(11) | ConvertFrom-Json
    if($r.tcp -ne $want -or $r.udp -ne $want){throw "Wrong traffic result at $stage"}
    Write-Output "$stage TCP/UDP=$want"
    Remove-Item Env:IRVIO_WINDOWS_WAN_HELPER,Env:IRVIO_WINDOWS_WAN_STAGE
}
$p=$null
try{
    Probe 'crash-baseline' $true
    $env:IRVIO_WINDOWS_PROTECTION_CRASH='hold'
    $start=[Diagnostics.ProcessStartInfo]::new($exe,'-test.run=^TestWindowsNativeProtectionCrash$ -test.v -test.timeout=4m')
    $start.UseShellExecute=$false;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $p=[Diagnostics.Process]::Start($start);$stderr=$p.StandardError.ReadToEndAsync()
    $ready=$false
    while(-not $ready){$line=$p.StandardOutput.ReadLineAsync();if(-not $line.Wait(15000) -or $null -eq $line.Result){throw 'No protection readiness'};if($line.Result -eq 'PROTECTION_HOLD_READY'){$ready=$true}}
    Probe 'crash-before-kill' $false
    $nic=@(Get-CimInstance Win32_NetworkAdapterConfiguration -Filter 'IPEnabled=True AND DHCPEnabled=True' | Where-Object {$_.IPAddress -contains '10.0.2.15'})
    if($nic.Count -ne 1){throw 'Expected disposable QEMU DHCP interface missing'}
    $before=$nic[0].DHCPLeaseObtained
    $renew=Invoke-CimMethod -InputObject $nic[0] -MethodName RenewDHCPLease -OperationTimeoutSec 30
    if($renew.ReturnValue -ne 0){throw "DHCP renewal failed under protection: $($renew.ReturnValue)"}
    $after=Get-CimInstance Win32_NetworkAdapterConfiguration -Filter "Index=$($nic[0].Index)"
    if(!$after.DHCPLeaseObtained -or $after.DHCPLeaseObtained -le $before){throw 'DHCP lease did not advance'}
    [pscustomobject]@{before=$before;after=$after.DHCPLeaseObtained;expires=$after.DHCPLeaseExpires;address=$after.IPAddress;return_value=$renew.ReturnValue} | ConvertTo-Json | Set-Content ($Evidence+'\dhcp.json')
    Write-Output 'DHCP renewal under full policy PASS; lease timestamp advanced'
    Probe 'crash-after-dhcp' $false
    $p.Kill();$p.WaitForExit()
    Probe 'crash-after-kill' $false
    $env:IRVIO_WINDOWS_PROTECTION_CRASH='adopt'
    & $exe '-test.run=^TestWindowsNativeProtectionCrash$' '-test.v' '-test.timeout=30s'
    if($LASTEXITCODE){throw 'Adoption failed'}
    Probe 'crash-restored' $true
    Write-Output 'Real process kill, fail-closed, controller adoption and cleanup PASS'
}finally{
    Remove-Item Env:IRVIO_WINDOWS_PROTECTION_CRASH,Env:IRVIO_WINDOWS_WAN_HELPER,Env:IRVIO_WINDOWS_WAN_STAGE -ErrorAction SilentlyContinue
    if($p){if(-not $p.HasExited){$p.Kill();$p.WaitForExit()};$stderr.Result | Set-Content ($Evidence+'\crash-stderr.txt');$p.Dispose()}
    $env:IRVIO_WINDOWS_PROTECTION_CLEANUP='1'
    & $exe '-test.run=^TestWindowsNativeProtectionCleanup$' '-test.v'
    $code=$LASTEXITCODE
    Remove-Item Env:IRVIO_WINDOWS_PROTECTION_CLEANUP
    if($code){throw 'Crash fixture cleanup failed'}
}
