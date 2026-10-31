# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][string]$Payload,
 [Parameter(Mandatory=$true)][string]$ManifestSHA256,
 [Parameter(Mandatory=$true)][string]$DesktopSID,
 [switch]$DevelopmentUnsigned,
 [switch]$RemoveProfile
)
$ErrorActionPreference='Stop'
if($DesktopSID -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$'){throw 'Explicit desktop user SID required'}
& (Join-Path $PSScriptRoot 'uninstall-desktop.ps1') -Payload $Payload -ManifestSHA256 $ManifestSHA256 -DesktopSID $DesktopSID -DevelopmentUnsigned:$DevelopmentUnsigned -RemoveProfile:$RemoveProfile
