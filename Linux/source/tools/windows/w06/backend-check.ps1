# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$TestBinary,
 [Parameter(Mandatory=$true)][string]$VendorDirectory,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [Parameter(Mandatory=$true)][string]$SourceIPv4,
 [Parameter(Mandatory=$true)][string]$PublicDNSOracle,
 [switch]$ReuseLabPayload
)
$ErrorActionPreference='Stop'
$env:IRVIO_WINDOWS_NAT_PORT_SOURCE=$SourceIPv4
$env:IRVIO_WINDOWS_NAT_DNS_ORACLE=$PublicDNSOracle
try {
 & (Join-Path $PSScriptRoot 'module-check.ps1') -TestBinary $TestBinary -VendorDirectory $VendorDirectory -Evidence $Evidence -ReuseLabPayload:$ReuseLabPayload
 $log=Get-Content -Raw (Join-Path $Evidence 'running.log')
 if(([regex]::Matches($log,'native_backend_dns_replies=5 leases=5 shared_inner_port=30000 cleanup=PASS')).Count -ne 3){throw 'Missing native five-lease backend evidence'}
 if(([regex]::Matches($log,'--- PASS: TestNativeBackendFiveLeaseDNS')).Count -ne 3){throw 'Native backend skipped or failed'}
 if(([regex]::Matches($log,'--- PASS: TestNativeEncryptedLeaseNAT')).Count -ne 3){throw 'Encrypted native backend skipped or failed'}
 Write-Output 'Native backend: 15 translated public DNS replies, revoke/quarantine/cleanup PASS'
 Write-Output 'Encrypted backend: five AWG peers, 42 public DNS replies, Core renew/RELEASE isolation PASS'
} finally {
 Remove-Item Env:IRVIO_WINDOWS_NAT_PORT_SOURCE -ErrorAction SilentlyContinue
 Remove-Item Env:IRVIO_WINDOWS_NAT_DNS_ORACLE -ErrorAction SilentlyContinue
}
