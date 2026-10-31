# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Observer,
 [Parameter(Mandatory=$true)][string]$Evidence
)
$root=$Evidence
$ErrorActionPreference='Stop'

if(Test-Path $root){throw 'Existing evidence'}
New-Item -ItemType Directory $root|Out-Null
$job=Start-Job -FilePath $Observer -ArgumentList "$root\observer",15,51822
$listener=$null
try {
 $deadline=[DateTime]::UtcNow.AddSeconds(20)
 while(-not(Test-Path "$root\observer\ready.txt")){
  if($job.State -ne 'Running' -or [DateTime]::UtcNow -gt $deadline){Receive-Job $job;throw 'Observer startup failed'}
  Start-Sleep -Milliseconds 200
 }
 Start-Sleep -Seconds 2
 $listener=New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback,51822)
 $listener.ExclusiveAddressUse=$true
 $listener.Start()
 Start-Sleep -Seconds 2
 $listener.Stop();$listener=$null
 Start-Sleep -Seconds 2
 New-Item -ItemType File "$root\observer\stop"|Out-Null
 $null=Wait-Job $job -Timeout 20
 Receive-Job $job
 if($job.State -ne 'Completed'){throw 'Observer did not complete'}
 $text=Get-Content -Raw "$root\observer\tcp-transitions.tsv"
 if($text -notmatch '127.0.0.1:51822>0.0.0.0:0 state=2 pid=' -or $text -notmatch 'OBSERVER_STOPPED'){throw 'Listener not observed'}
 $result=Get-Content -Raw "$root\observer\observer-result.json"|ConvertFrom-Json
 if($result.tcp_error){throw $result.tcp_error}
 if($text -notmatch 'pid=[0-9]+\r?\n[^\t]+\t[0-9]+\t\r?\n'){throw 'Listener close not observed'}
 @{result='PASS';listener_seen=$true;closed_listener_observed=$true}|ConvertTo-Json|Set-Content "$root\result.json"
 Get-Content "$root\result.json"
}finally{
 if($listener){$listener.Stop()}
 if($job.State -eq 'Running'){Stop-Job $job}
 Remove-Job $job
}
