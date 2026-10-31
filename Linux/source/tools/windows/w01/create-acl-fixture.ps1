# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

$ErrorActionPreference='Stop'
if ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne 'S-1-5-18') { throw 'Run fixture creation as SYSTEM in the disposable VM' }
if (Test-Path C:\IRVIO-Lab\W01-service-acl) { throw 'Fixture path already exists' }
$env:IRVIO_WINFILE_ACL_FIXTURE='C:\IRVIO-Lab\W01-service-acl'
whoami
& C:\IRVIO-Lab\W01\winfile.test.exe '-test.run=^TestServiceACLFixture$' '-test.v'
if ($LASTEXITCODE) { throw 'SYSTEM fixture creation failed' }
