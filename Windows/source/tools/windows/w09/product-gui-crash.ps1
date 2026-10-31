# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$API,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$InstallDirectory
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$pipe='\\.\pipe\IRVIO.Control'
if(Test-Path $Evidence){throw 'Evidence exists'}
if($InstallDirectory -ne 'C:\Program Files\IRVIO-W09'){throw 'Disposable W09 fixture required'}
$service=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
$ui=@(Get-CimInstance Win32_Process -Filter "Name='irvio.exe'")
if($service.State -ne 'Running' -or $service.PathName -ne ('"'+$InstallDirectory+'\irviod.exe"')){throw 'Unexpected service target'}
if($ui.Count -ne 1 -or $ui[0].ExecutablePath -ne "$InstallDirectory\irvio.exe"){throw 'Unexpected GUI process'}
New-Item -ItemType Directory $Evidence | Out-Null
function Status {
 $raw=& $API status $pipe
 if($LASTEXITCODE){throw 'Status failed'}
 $raw | Add-Content "$Evidence\status.ndjson"
 return ($raw | ConvertFrom-Json)
}
$before=Status
if($before.state -ne 'CONNECTION_STATE_CONNECTED' -or -not $before.connection.connected_since_unix_ms){throw 'Connected session required'}
@{ui_pid=$ui[0].ProcessId;daemon_pid=$service.ProcessId;connection=$before.connection} | ConvertTo-Json | Set-Content "$Evidence\before.json"
Stop-Process -Id $ui[0].ProcessId -Force
for($i=0;$i -lt 15;$i++) {
 $current=Get-CimInstance Win32_Service -Filter "Name='IRVIO'"
 $status=Status
 if($current.State -ne 'Running' -or $current.ProcessId -ne $service.ProcessId -or $status.state -ne 'CONNECTION_STATE_CONNECTED' -or $status.connection.connected_since_unix_ms -ne $before.connection.connected_since_unix_ms){throw 'GUI death changed daemon or VPN session'}
 if($i -ge 5 -and (Get-Process irvio,irvio-ui-bridge -ErrorAction SilentlyContinue)){throw 'GUI or bridge survived GUI termination'}
 Start-Sleep -Seconds 1
}
@{result='PASS';daemon_pid=$service.ProcessId;observations=15;gui_and_helper_absent=$true;connected_since=$before.connection.connected_since_unix_ms} | ConvertTo-Json | Set-Content "$Evidence\verdict.json"
'GUI CRASH PASS: same daemon and VPN session; GUI/helper processes gone'
