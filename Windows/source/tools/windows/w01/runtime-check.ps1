# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($Evidence -notmatch '^[A-Za-z]:\\[A-Za-z0-9_\\.-]+$'){throw 'Use an absolute evidence path without spaces or shell metacharacters'}
if(Test-Path $Evidence){throw 'Evidence exists'}
New-Item -ItemType Directory $Evidence | Out-Null
& cmd.exe /d /c "go vet ./internal/runtime ./internal/socketmux ./internal/transportgen > $Evidence\vet.log 2>&1"
if($LASTEXITCODE){throw 'Native vet failed'}
& cmd.exe /d /c "go test -gcflags=all=-d=checkptr=2 -shuffle=on -count=10 -v -timeout=8m ./internal/runtime ./internal/socketmux ./internal/transportgen > $Evidence\test.log 2>&1"
$code=$LASTEXITCODE
Get-Content ($Evidence+'\test.log') -Tail 40
if($code){throw 'Native shared runtime tests failed; evidence retained'}
Write-Output 'Native shared runtime tests PASS (not full daemon acceptance)'
