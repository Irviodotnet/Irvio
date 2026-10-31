# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$env:Path = 'C:\Tools\go\bin;C:\Program Files\Git\cmd;' + $env:Path
Set-Location C:\src\irvio
New-Item -ItemType Directory -Force C:\IRVIO-Lab\W00 | Out-Null
& go.exe fmt ./tools/windows/w00/wintun
& cmd.exe /d /c 'go build -o C:\IRVIO-Lab\W00\wintun-probe.exe ./tools/windows/w00/wintun > C:\IRVIO-Lab\W00\wintun-build.log 2>&1'
$code = $LASTEXITCODE
Get-Content C:\IRVIO-Lab\W00\wintun-build.log
if ($code) { throw "Wintun build failed: $code" }
& cmd.exe /d /c 'go build -o C:\IRVIO-Lab\W00\irviod-baseline.exe ./cmd/irviod > C:\IRVIO-Lab\W00\compiler-baseline.log 2>&1'
Write-Output "baseline_compiler_exit=$LASTEXITCODE"
Get-Content C:\IRVIO-Lab\W00\compiler-baseline.log
