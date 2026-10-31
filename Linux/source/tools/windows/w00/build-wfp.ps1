# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$root = 'C:\IRVIO-Lab\W00'
$dev = 'C:\Program Files\Microsoft Visual Studio\2022\Community\Common7\Tools\VsDevCmd.bat'
New-Item -ItemType Directory -Force $root | Out-Null
$line = 'call "' + $dev + '" -arch=x64 -host_arch=x64 && cd /d ' + $root + ' && cl /nologo /W4 /EHsc /std:c++17 C:\IRVIO-Lab\windows\w00\wfp-lifetime.cpp /Fe:wfp-lifetime.exe /link Fwpuclnt.lib Rpcrt4.lib'
& cmd.exe /d /c $line
if ($LASTEXITCODE) { throw "WFP probe build failed: $LASTEXITCODE" }
