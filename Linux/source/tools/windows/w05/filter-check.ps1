# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([string]$Evidence='C:\IRVIO-Lab\evidence\w05-filter-live')
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(Test-Path $Evidence){throw 'Evidence already exists'}
New-Item -ItemType Directory $Evidence | Out-Null
$exe='C:\IRVIO-Lab\W05\filterprobe.exe'
$records=[System.Collections.Generic.List[object]]::new()
$process=$null;$tcp=$null;$udp=$null;$owned=$false;$failure=$null
function New-TCP {
    $c=[Net.Sockets.TcpClient]::new()
    try{
        $c.ReceiveTimeout=1000;$c.SendTimeout=1000
        if(-not $c.ConnectAsync('192.168.23.2',49494).Wait(1500)){throw 'TCP connect timeout'}
        return $c
    }catch{$c.Dispose();throw}
}
function TCP-Echo($client,[string]$stage){
    $token='irvio-nat-wfp-'+$stage+'-'+[Guid]::NewGuid().ToString('N')
    try{
        $bytes=[Text.Encoding]::ASCII.GetBytes($token+"`n")
        $stream=$client.GetStream();$stream.Write($bytes,0,$bytes.Length)
        $reply=[byte[]]::new($bytes.Length);$offset=0
        while($offset -lt $reply.Length){$n=$stream.Read($reply,$offset,$reply.Length-$offset);if($n -le 0){throw 'TCP closed'};$offset+=$n}
        $ok=([Text.Encoding]::ASCII.GetString($reply) -eq ($token+"`n"))
    }catch{$ok=$false}
    $records.Add([pscustomobject]@{stage=$stage;protocol='tcp';token=$token;echo=$ok;at=(Get-Date).ToUniversalTime().ToString('o')})
    return $ok
}
function Fresh-TCP([string]$stage){
    $c=$null
    try{$c=New-TCP;return TCP-Echo $c $stage}catch{$records.Add([pscustomobject]@{stage=$stage;protocol='tcp';echo=$false;at=(Get-Date).ToUniversalTime().ToString('o')});return $false}
    finally{if($c){$c.Dispose()}}
}
function UDP-Echo([string]$stage){
    $token='irvio-nat-wfp-'+$stage+'-'+[Guid]::NewGuid().ToString('N')
    try{
        $bytes=[Text.Encoding]::ASCII.GetBytes($token)
        $udp.Send($bytes,$bytes.Length) | Out-Null
        $from=[Net.IPEndPoint]::new([Net.IPAddress]::Any,0)
        $reply=$udp.Receive([ref]$from)
        $ok=([Text.Encoding]::ASCII.GetString($reply) -eq $token)
    }catch{$ok=$false}
    $records.Add([pscustomobject]@{stage=$stage;protocol='udp';token=$token;echo=$ok;at=(Get-Date).ToUniversalTime().ToString('o')})
    return $ok
}
try{
    $before=& $exe inspect | ConvertFrom-Json
    if($LASTEXITCODE -or $before.Installed){throw 'Existing lab policy'}
    $tcp=New-TCP
    $udp=[Net.Sockets.UdpClient]::new();$udp.Client.ReceiveTimeout=1000;$udp.Connect('192.168.23.2',49495)
    if(-not (TCP-Echo $tcp 'baseline') -or -not (UDP-Echo 'baseline')){throw 'Independent echo oracle unavailable'}
    $start=[Diagnostics.ProcessStartInfo]::new($exe,'install-hold')
    $start.UseShellExecute=$false;$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
    $process=[Diagnostics.Process]::Start($start);$stderr=$process.StandardError.ReadToEndAsync()
    $owned=$true
    $ready=$process.StandardOutput.ReadLineAsync()
    if(-not $ready.Wait(15000) -or -not $ready.Result){throw 'Filter readiness failed'}
    $ready.Result | Set-Content -Encoding UTF8 ($Evidence+'\ready.json')
    $snapshot=$ready.Result | ConvertFrom-Json
    if(-not $snapshot.Active -or $snapshot.Rules.Count -ne 4){throw 'Filter readback incomplete'}
    if((Fresh-TCP 'protected-new') -or (TCP-Echo $tcp 'protected-held') -or (UDP-Echo 'protected-held')){throw 'Protected oracle traffic escaped'}
    $process.Kill();$process.WaitForExit()
    $after=& $exe inspect | ConvertFrom-Json
    if($LASTEXITCODE -or -not $after.Active){throw 'Policy lost after process kill'}
    $after | ConvertTo-Json -Depth 8 | Set-Content -Encoding UTF8 ($Evidence+'\after-kill.json')
    if((Fresh-TCP 'after-kill') -or (UDP-Echo 'after-kill')){throw 'Post-crash oracle traffic escaped'}
    & $exe remove
    if($LASTEXITCODE){throw 'Policy removal failed'}
    if(-not (Fresh-TCP 'restored')){throw 'TCP not restored'}
    $udp.Dispose();$udp=[Net.Sockets.UdpClient]::new();$udp.Client.ReceiveTimeout=1000;$udp.Connect('192.168.23.2',49495)
    if(-not (UDP-Echo 'restored')){throw 'UDP not restored'}
}catch{$failure=$_.Exception.Message;throw}
finally{
    if($process){if(-not $process.HasExited){$process.Kill();$process.WaitForExit()};$stderr.Result | Set-Content ($Evidence+'\probe-stderr.txt');$process.Dispose()}
    if($tcp){$tcp.Dispose()};if($udp){$udp.Dispose()}
    if($owned){& $exe remove;if($LASTEXITCODE){throw 'Final owned policy cleanup failed'}}
    $end=& $exe inspect | ConvertFrom-Json
    if($LASTEXITCODE -or $end.Installed){throw 'Policy residue'}
    [pscustomobject]@{failure=$failure;records=$records;cleanup=$true;final=$end;secure_boot=(Confirm-SecureBootUEFI);firewall=@(Get-NetFirewallProfile | Select-Object Name,Enabled)} | ConvertTo-Json -Depth 8 | Set-Content -Encoding UTF8 ($Evidence+'\result.json')
}
Write-Output 'W05 persistent filter, held flow, process-kill and cleanup PASS'
