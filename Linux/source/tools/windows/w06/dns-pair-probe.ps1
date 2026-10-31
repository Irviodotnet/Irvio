# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][System.Net.IPAddress]$SourceAddress,
 [Parameter(Mandatory=$true)][System.Net.IPAddress]$Resolver,
 [ValidateRange(1,50)][int]$Iterations=10
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(Get-Service IRVIO -ErrorAction SilentlyContinue){throw 'Direct baseline requires a cleaned product fixture'}
if($SourceAddress.AddressFamily -ne 'InterNetwork' -or $Resolver.AddressFamily -ne 'InterNetwork'){throw 'IPv4 lab endpoints required'}
New-Item -ItemType Directory $Evidence | Out-Null
function New-Query([int]$ID,[int]$Type) {
 $bytes=New-Object 'System.Collections.Generic.List[byte]'
 $bytes.AddRange([byte[]]@(($ID -shr 8),($ID -band 255),1,0,0,1,0,0,0,0,0,0))
 foreach($label in @('speed','cloudflare','com')) {
  $bytes.Add([byte]$label.Length)
  $bytes.AddRange([Text.Encoding]::ASCII.GetBytes($label))
 }
 $bytes.AddRange([byte[]]@(0,0,$Type,0,1))
 return ,$bytes.ToArray()
}
foreach($gap in @(0,20)) {
 for($i=0;$i -lt $Iterations;$i++) {
  $client=New-Object Net.Sockets.UdpClient ([Net.Sockets.AddressFamily]::InterNetwork)
  try {
   $client.Client.Bind((New-Object Net.IPEndPoint($SourceAddress,0)))
   $client.Connect($Resolver,53)
   $port=$client.Client.LocalEndPoint.Port
   $aID=4096+$gap*100+$i*2
   $aaaaID=$aID+1
   $a=New-Query $aID 1
   $aaaa=New-Query $aaaaID 28
   $clock=[Diagnostics.Stopwatch]::StartNew()
   [void]$client.Send($a,$a.Length)
   if($gap){Start-Sleep -Milliseconds $gap}
   [void]$client.Send($aaaa,$aaaa.Length)
   $seen=@{}
   while($clock.ElapsedMilliseconds -lt 800 -and $seen.Count -lt 2) {
    if(-not $client.Client.Poll(20000,[Net.Sockets.SelectMode]::SelectRead)){continue}
    $remote=New-Object Net.IPEndPoint([Net.IPAddress]::Any,0)
    $packet=$client.Receive([ref]$remote)
    if($packet.Length -lt 12 -or ($packet[2] -band 128) -eq 0){continue}
    $id=([int]$packet[0] -shl 8)+$packet[1]
    if($id -ne $aID -and $id -ne $aaaaID){continue}
    $seen[$id]=@{elapsed_ms=$clock.Elapsed.TotalMilliseconds;rcode=($packet[3] -band 15)}
   }
   @{iteration=$i;gap_ms=$gap;source=$SourceAddress.ToString();resolver=$Resolver.ToString();port=$port;a_id=$aID;aaaa_id=$aaaaID;a=$seen[$aID];aaaa=$seen[$aaaaID]} | ConvertTo-Json -Depth 4 -Compress | Add-Content "$Evidence\pairs.ndjson"
  } finally {$client.Dispose()}
 }
}
'DIRECT DNS PAIR MEASUREMENT COMPLETE; missing replies are results, not a PASS claim'
