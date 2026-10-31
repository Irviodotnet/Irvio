# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [string]$Probe='C:\IRVIO-Lab\W06\natdual.exe',
    [string]$Output='C:\IRVIO-Lab\evidence\w06-natdual\automatic.json',
    [switch]$ExplicitExternal,
    [switch]$NoNat,
    [switch]$NoForwarding,
    [switch]$TunOnly,
    [switch]$PhysicalOnly,
    [switch]$VerifyPinning,
    [switch]$ForwardInject,
    [switch]$InjectionNegativeControls,
    [switch]$InjectionStress,
    [switch]$OnLink
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$name='IRVIO-NATDUAL'
$rule='IRVIO-NATDUAL-ORACLE'
$prefix='10.253.240.0/24'
$nextHop=if($OnLink){'0.0.0.0'}else{'10.253.241.1'}
if(@($NoForwarding,$TunOnly,$PhysicalOnly | Where-Object {$_}).Count -gt 1){throw 'Conflicting forwarding modes'}
if($ForwardInject -and (-not $NoNat -or -not $NoForwarding)){throw 'ForwardInject requires NoNat and NoForwarding'}
if($InjectionNegativeControls -and -not $ForwardInject){throw 'InjectionNegativeControls requires ForwardInject'}
if($InjectionStress -and -not $ForwardInject){throw 'InjectionStress requires ForwardInject'}
if($InjectionNegativeControls -and (Get-NetAdapter -IncludeHidden | Where-Object ifIndex -eq 2147483646)){throw 'Invalid-index fixture is present'}
if(Test-Path $Output){throw 'Evidence already exists'}
if(@(Get-NetNat).Count){throw 'Existing NAT: refusing probe'}
if(Get-NetAdapter -Name $name -IncludeHidden -ErrorAction SilentlyContinue){throw 'Existing lab adapter'}
if(Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue){throw 'Existing lab rule'}
foreach($pfx in @($prefix,'10.253.241.0/24','192.168.23.2/32')){
    if(Get-NetRoute -DestinationPrefix $pfx -ErrorAction SilentlyContinue){throw ('Existing lab route '+$pfx)}
}
if((Get-AuthenticodeSignature (Join-Path (Split-Path $Probe) 'wintun.dll')).Status -ne 'Valid'){throw 'Wintun signature invalid'}
New-Item -ItemType Directory -Force (Split-Path $Output) | Out-Null
$records=[System.Collections.Generic.List[object]]::new()
$baseline=@(Get-NetRoute | Where-Object DestinationPrefix -In @('0.0.0.0/0','::/0') | Sort-Object InterfaceIndex,DestinationPrefix | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric) | ConvertTo-Json -Compress
$p=$null;$physical=$null;$createdNat=$false;$createdRule=$false;$createdRoute=$false;$observed='NOT RUN';$lab='FAIL'
function Reply([int]$Timeout=15000) {
    $task=$p.StandardOutput.ReadLineAsync()
    if(-not $task.Wait($Timeout)-or -not $task.Result){throw 'Probe timed out or exited'}
    $value=$task.Result | ConvertFrom-Json
    $records.Add($value)
    return $value
}
function Request([string]$command){$p.StandardInput.WriteLine($command);$p.StandardInput.Flush();$timeout=if($command -eq 'forward-stress'){60000}else{15000};return Reply $timeout}
try{
    $start=[Diagnostics.ProcessStartInfo]::new($Probe)
    $start.UseShellExecute=$false;$start.RedirectStandardInput=$true;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $p=[Diagnostics.Process]::Start($start);$stderr=$p.StandardError.ReadToEndAsync()
    $ready=Reply
    if($ready.stage -ne 'ready'){throw 'No native readiness'}
    $physical=Get-NetIPInterface -InterfaceIndex $ready.physical.Binding.Index -AddressFamily IPv4 -PolicyStore ActiveStore
    $initial=Request 'physical'
    if(-not $initial.success -or @($initial.packets).Count -ne 0){throw 'Initial physical oracle failed'}
    if(-not $NoForwarding -and -not $TunOnly){
        Set-NetIPInterface -InterfaceIndex $physical.InterfaceIndex -AddressFamily IPv4 -Forwarding Enabled -PolicyStore ActiveStore
    }
    if(-not $NoForwarding -and -not $PhysicalOnly){
        Set-NetIPInterface -InterfaceIndex $ready.ifindex -AddressFamily IPv4 -Forwarding Enabled -PolicyStore ActiveStore
    }
    $records.Add([pscustomobject]@{stage='interfaces';interfaces=@(Get-NetIPInterface -AddressFamily IPv4 | Where-Object InterfaceIndex -In @($physical.InterfaceIndex,$ready.ifindex) | Select-Object InterfaceIndex,Forwarding,WeakHostSend,WeakHostReceive)})
    if(-not $NoNat){
    if($ExplicitExternal){New-NetNat -Name $name -InternalIPInterfaceAddressPrefix $prefix -ExternalIPInterfaceAddressPrefix ($ready.physical.Binding.Source+'/32') | Out-Null}
    else{New-NetNat -Name $name -InternalIPInterfaceAddressPrefix $prefix | Out-Null}
    $createdNat=$true
    New-NetFirewallRule -Name $rule -DisplayName $rule -Direction Inbound -Action Allow -Protocol UDP -LocalAddress 192.168.23.2 -RemoteAddress $prefix -LocalPort 49495 -Profile Any | Out-Null
    $createdRule=$true
    $before=Request 'baseline'
    if($before.responses -ne 5 -or $before.upstream_captures -ne 0){throw 'Baseline NAT/independent oracle failed'}
    }
    New-NetRoute -InterfaceIndex $ready.ifindex -DestinationPrefix '192.168.23.2/32' -NextHop $nextHop -RouteMetric 1 -PolicyStore ActiveStore | Out-Null
    $createdRoute=$true
    $route=Find-NetRoute -RemoteIPAddress 192.168.23.2
    if(@($route.InterfaceIndex | Sort-Object -Unique).Count -ne 1 -or $route[0].InterfaceIndex -ne $ready.ifindex){throw 'Upstream test route not selected'}
    $records.Add([pscustomobject]@{stage='route';route=@($route | Select-Object InterfaceIndex,NextHop,DestinationPrefix,IPAddress)})
    $ordinary=Request 'ordinary'
    if($ordinary.success -or @($ordinary.packets).Count -lt 1){throw 'Ordinary traffic negative control missed upstream'}
    foreach($command in @('physical','physical-repin','physical-packet','physical-mux')){
        $underlay=Request $command
        if($VerifyPinning){
            if(@($underlay.packets).Count -ne 0){throw 'Pinned request entered upstream tunnel'}
            if($NoForwarding -or $TunOnly){if(-not $underlay.success){throw 'Strong-host bound request failed'}}
            elseif($underlay.success -or $underlay.error -notlike '*requires forwarding and weak-host send disabled*'){throw 'Unsafe forwarding mode not rejected'}
        }
    }
    if($ForwardInject){
        $injected=Request 'forward-inject'
        $translated=Request 'forward-nat'
        $tcp=Request 'forward-tcp'
        if($InjectionStress){$stress=Request 'forward-stress';if(-not $stress.success){throw 'Forward-injection lifecycle stress failed'}}
        if($InjectionNegativeControls){
            $invalid=Request 'forward-invalid'
            if($invalid.responses -ne 0 -or $invalid.upstream_captures -ne 0){throw 'Invalid injection index fell back to another path'}
            $tunRequest=Request 'forward-tun'
            if($tunRequest.responses -ne 0 -or $tunRequest.upstream_captures -ne 5){throw 'Explicit TUN injection control did not follow chosen interface'}
        }
        $observed=if($injected.responses -eq 5 -and $injected.upstream_captures -eq 0 -and $translated.responses -eq 5 -and $translated.upstream_captures -eq 0 -and $tcp.success -and $tcp.upstream_captures -eq 0){'FORWARD INJECTION AND UDP/TCP TRANSLATION RETAINED PHYSICAL WAN'}else{'FORWARD INJECTION OR TRANSLATION FAILED'}
    }
    if(-not $NoNat){
    $routed=Request 'routed'
    $observed=if($routed.responses -eq 5 -and $routed.upstream_captures -eq 0){'PHYSICAL WAN RETAINED'}elseif($routed.upstream_captures -gt 0){'DOWNSTREAM ENTERED UPSTREAM TUNNEL'}else{'DOWNSTREAM BLOCKED OR LOST'}
    }
    Remove-NetRoute -InterfaceIndex $ready.ifindex -DestinationPrefix '192.168.23.2/32' -NextHop $nextHop -Confirm:$false
    $createdRoute=$false
    if(-not $NoNat){
    $restored=Request 'restored'
    if($restored.responses -ne 5 -or $restored.upstream_captures -ne 0){throw 'NAT did not recover after removing upstream route'}
    $records.Add([pscustomobject]@{stage='sessions';sessions=@(Get-NetNatSession | Select-Object Protocol,InternalSourceAddress,InternalSourcePort,ExternalSourceAddress,ExternalSourcePort,ExternalDestinationAddress,ExternalDestinationPort)})
    }
    $lab='PASS'
}catch{
    $failure=$_.ToString()
    throw
}finally{
    if($createdRoute){Remove-NetRoute -InterfaceIndex $ready.ifindex -DestinationPrefix '192.168.23.2/32' -NextHop $nextHop -Confirm:$false}
    if($createdRule){Remove-NetFirewallRule -Name $rule}
    if($createdNat){Remove-NetNat -Name $name -Confirm:$false}
    if($physical){Set-NetIPInterface -InterfaceIndex $physical.InterfaceIndex -AddressFamily IPv4 -Forwarding $physical.Forwarding -PolicyStore ActiveStore}
    if($p){if(-not $p.HasExited){$p.StandardInput.WriteLine('quit');if(-not $p.WaitForExit(10000)){$p.Kill();$p.WaitForExit()}};$exitCode=$p.ExitCode;$errorLog=$stderr.Result;$p.Dispose()}
    $after=@(Get-NetRoute | Where-Object DestinationPrefix -In @('0.0.0.0/0','::/0') | Sort-Object InterfaceIndex,DestinationPrefix | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric) | ConvertTo-Json -Compress
    $clean=($baseline -eq $after) -and -not (Get-NetAdapter -Name $name -IncludeHidden -ErrorAction SilentlyContinue) -and -not (Get-NetNat | Where-Object Name -eq $name) -and -not (Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue)
    if($physical){$clean=$clean -and ((Get-NetIPInterface -InterfaceIndex $physical.InterfaceIndex -AddressFamily IPv4 -PolicyStore ActiveStore).Forwarding -eq $physical.Forwarding)}
    [pscustomobject]@{lab=$lab;explicit_external=[bool]$ExplicitExternal;no_nat=[bool]$NoNat;no_forwarding=[bool]$NoForwarding;tun_only=[bool]$TunOnly;physical_only=[bool]$PhysicalOnly;verify_pinning=[bool]$VerifyPinning;next_hop=$nextHop;observation=$observed;cleanup=$clean;exit_code=$exitCode;records=$records;failure=$failure;stderr=$errorLog} | ConvertTo-Json -Depth 15 | Set-Content -Encoding UTF8 $Output
    if(-not $clean){throw 'Lab cleanup failed'}
}
Get-Content $Output
