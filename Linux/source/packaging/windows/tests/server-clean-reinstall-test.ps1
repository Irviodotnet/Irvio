# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][string]$Scripts,[Parameter(Mandatory=$true)][string]$Payload,[Parameter(Mandatory=$true)][string]$SetupEXE,[Parameter(Mandatory=$true)][string]$Evidence)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. (Join-Path $Scripts 'server-installation.ps1')
New-IRVIOServerDirectory $Evidence
$old=Read-IRVIOServerInstallation
if((Get-UninstallSCMService IRVIO).State -ne 1 -or (Get-UninstallSCMService IRVIO).Start -ne 4){throw 'Stopped disabled disposable server required'}
$root=$old.root
$identity=(Get-FileHash "$root\profile\identity.key").Hash
$bootstrapHash=(Get-FileHash "$root\server-bootstrap.txt").Hash
$hash=(Get-FileHash "$Payload\payload-manifest.json").Hash
& "$Scripts\uninstall-desktop.ps1" -Role server -Payload $Payload -ManifestSHA256 $hash -DevelopmentUnsigned *> "$Evidence\retain-old-profile.log"
$retained=Join-Path $Evidence 'private-retained-server'
Move-Item -LiteralPath $root -Destination $retained
foreach($path in @($root,$old.program,$old.driver)){if(Test-Path $path){throw 'Fresh installation path still exists'}}
foreach($name in @('IRVIO','IRVIO-Recovery','WinDivert')){if($null -ne (Get-UninstallSCMService $name)){throw 'Fresh installation service still exists'}}
@{canonical_paths_absent=$true;services_absent=$true}|ConvertTo-Json|Set-Content "$Evidence\fresh-preflight.json"
& "$Scripts\tests\setup-server-test.ps1" -Scripts $Scripts -SetupEXE $SetupEXE -PreviousSetupEXE $SetupEXE -BootstrapFile "$retained\server-bootstrap.txt" -Evidence "$Evidence\fresh" -FreshOnly *> "$Evidence\fresh.log"
$fresh=Read-IRVIOServerInstallation
if((Get-FileHash "$root\profile\identity.key").Hash -ceq $identity){throw 'Fresh installation reused the retained identity'}
if((Get-FileHash "$root\server-bootstrap.txt").Hash -cne $bootstrapHash){throw 'Fresh installation persisted wrong bootstrap'}
& "$Scripts\uninstall-desktop.ps1" -Role server -Payload $Payload -ManifestSHA256 $hash -DevelopmentUnsigned -RemoveProfile *> "$Evidence\remove-fresh-test-profile.log"
Assert-IRVIOErasedProfile $root
Move-Item -LiteralPath $root -Destination (Join-Path $Evidence 'private-fresh-removal-state')
Move-Item -LiteralPath $retained -Destination $root
$bundle=[IO.File]::ReadAllText("$root\server-bootstrap.txt")
& "$Scripts\install-or-update-server.ps1" -Payload $Payload -ManifestSHA256 $hash -Bootstrap $bundle -DevelopmentUnsigned *> "$Evidence\restore-original.log"
Read-IRVIOServerInstallation|Out-Null
if((Get-FileHash "$root\profile\identity.key").Hash -cne $identity -or (Get-FileHash "$root\server-bootstrap.txt").Hash -cne $bootstrapHash){throw 'Original server identity/bootstrap was not restored'}
Stop-IRVIOForMaintenance -Role server|Out-Null
try{Assert-IRVIOMaintenanceClean "$Payload\daemon\irviod.exe" (Get-FileHash "$Payload\daemon\irviod.exe").Hash -Role server|ConvertTo-Json|Set-Content "$Evidence\restored-cleanup.json"}
finally{Restore-IRVIOMaintenanceStartModes -Role server|Out-Null}
Invoke-IRVIOMaintenanceSC @('config','IRVIO','start=','disabled')
@{test='fresh-exe-server-install-and-retained-server-restoration';result='PASS';fresh_identity_distinct=$true;original_identity_restored=$true;fresh_test_profile_erased=$true}|ConvertTo-Json|Set-Content "$Evidence\result.json"
Get-FileHash $SetupEXE,$PSCommandPath|Select-Object Path,Hash|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'FRESH SERVER EXE INSTALL PASS; original lab identity restored; private state directories must not be exported'
