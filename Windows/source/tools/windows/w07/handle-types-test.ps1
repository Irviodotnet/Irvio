# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

$ErrorActionPreference='Stop'
$sampler=Join-Path $PSScriptRoot 'handle-types.ps1'
$null=& $sampler -ProcessID $PID
$events=@()
try {
 $before=& $sampler -ProcessID $PID
 for($i=0;$i -lt 64;$i++){$events+=New-Object System.Threading.EventWaitHandle($false,[System.Threading.EventResetMode]::ManualReset)}
 $during=& $sampler -ProcessID $PID
 $grown=@($during.Keys | Where-Object {($during[$_]-$before[$_]) -ge 64})
 if($grown.Count -ne 1){throw 'Sampler did not identify 64 retained Event handles'}
} finally {
 foreach($event in $events){$event.Dispose()}
}
$after=& $sampler -ProcessID $PID
if(($during[$grown[0]]-$after[$grown[0]]) -lt 64){throw 'Sampler did not observe explicit handle release'}
if((& $sampler -ProcessID 2147483647).Count){throw 'Nonexistent PID returned handles'}
"PASS: retained and released 64 handles; object type=$($grown[0])"
