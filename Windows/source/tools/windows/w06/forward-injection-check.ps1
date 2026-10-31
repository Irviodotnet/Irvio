# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
    [string]$Evidence='C:\IRVIO-Lab\evidence\w06-forward-injection-controls',
    [ValidateRange(1,10)][int]$Repetitions=3,
    [switch]$Stress
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$root='C:\IRVIO-Lab\W06-divert'
$zip='C:\IRVIO-Lab\irvio-windivert-2.2.2-A.zip'
$archiveHash='63cb41763bb4b20f600b6de04e991a9c2be73279e317d4d82f237b150c5f3f15'
$rule='IRVIO-WFPINJECT-ORACLE'
if(Test-Path $Evidence){throw 'Evidence already exists'}
if(Get-CimInstance Win32_SystemDriver | Where-Object {$_.Name -like '*WinDivert*' -or $_.PathName -like '*WinDivert*'}){throw 'Existing WinDivert installation'}
if(Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue){throw 'Existing oracle firewall rule'}
if((Get-FileHash $zip).Hash -ne $archiveHash){throw 'Vendor archive hash mismatch'}
New-Item -ItemType Directory -Path $Evidence | Out-Null
if(-not (Test-Path $root)){
    New-Item -ItemType Directory -Path $root | Out-Null
    Expand-Archive $zip -DestinationPath ($root+'\vendor')
    Copy-Item ($root+'\vendor\WinDivert-2.2.2-A\x64\WinDivert.dll'),($root+'\vendor\WinDivert-2.2.2-A\x64\WinDivert64.sys') $root
}
$verify=Join-Path $Evidence 'vendor-verified'
Expand-Archive $zip -DestinationPath $verify
foreach($file in @('WinDivert.dll','WinDivert64.sys')){
    if((Get-FileHash (Join-Path $root $file)).Hash -ne (Get-FileHash (Join-Path ($verify+'\WinDivert-2.2.2-A\x64') $file)).Hash){throw 'Existing driver payload differs from vendor archive'}
}
Get-AuthenticodeSignature ($root+'\WinDivert.dll'),($root+'\WinDivert64.sys') | Select-Object Path,@{n='status';e={$_.Status.ToString()}},StatusMessage,@{n='subject';e={$_.SignerCertificate.Subject}},@{n='thumbprint';e={$_.SignerCertificate.Thumbprint}} | ConvertTo-Json | Set-Content -Encoding UTF8 ($Evidence+'\signatures.json')
if((Get-AuthenticodeSignature ($root+'\WinDivert64.sys')).Status -ne 'Valid'){throw 'Driver signature is not valid'}
if(-not (Confirm-SecureBootUEFI)){throw 'Secure Boot is not enabled'}
$createdRule=$false;$completed=0;$failure=$null
try{
    New-NetFirewallRule -Name $rule -DisplayName $rule -Direction Inbound -Action Allow -Protocol UDP -RemoteAddress 192.168.23.2 -RemotePort 49495 -LocalAddress 10.0.2.15 -Program C:\IRVIO-Lab\W06\natdual-inject.exe -Profile Any | Out-Null
    $createdRule=$true
    for($iteration=1;$iteration -le $Repetitions;$iteration++){
        $output=Join-Path $Evidence ('probe-'+$iteration+'.json')
        & $PSScriptRoot\natdual-check.ps1 -Probe C:\IRVIO-Lab\W06\natdual-inject.exe -NoNat -NoForwarding -VerifyPinning -ForwardInject -InjectionNegativeControls -InjectionStress:$Stress -Output $output | Out-Null
        $result=Get-Content -Raw $output | ConvertFrom-Json
        if($result.observation -ne 'FORWARD INJECTION AND UDP/TCP TRANSLATION RETAINED PHYSICAL WAN' -or -not $result.cleanup -or $result.exit_code -ne 0){throw 'Forwarding experiment failed'}
        $completed++
    }
}catch{
    $failure=$_.ToString()
    throw
}finally{
    if($createdRule){Remove-NetFirewallRule -Name $rule}
    $drivers=@(Get-CimInstance Win32_SystemDriver | Where-Object {$_.Name -like '*WinDivert*' -or $_.PathName -like '*WinDivert*'})
    $drivers | Select-Object Name,State,PathName | ConvertTo-Json | Set-Content ($Evidence+'\driver-before-cleanup.json')
    foreach($driver in $drivers){
        $path=$driver.PathName.Replace('\??\','').Trim('"')
        if($path -ne ($root+'\WinDivert64.sys')){throw 'Unexpected driver path, refusing cleanup'}
        & sc.exe stop $driver.Name
        if($LASTEXITCODE -notin @(0,1060,1062)){throw 'Driver stop failed'}
        & sc.exe delete $driver.Name
        if($LASTEXITCODE -notin @(0,1060)){throw 'Driver removal failed'}
    }
    $remaining=@(Get-CimInstance Win32_SystemDriver | Where-Object {$_.Name -like '*WinDivert*' -or $_.PathName -like '*WinDivert*'})
    $state=[pscustomobject]@{completed=$completed;requested=$Repetitions;failure=$failure;secure_boot=(Confirm-SecureBootUEFI);defender=(Get-MpComputerStatus | Select-Object AntivirusEnabled,RealTimeProtectionEnabled);remaining_drivers=$remaining.Count;rule_remaining=[bool](Get-NetFirewallRule -Name $rule -ErrorAction SilentlyContinue)}
    $state | ConvertTo-Json -Depth 6 | Set-Content -Encoding UTF8 ($Evidence+'\cleanup.json')
    if($remaining.Count -or $state.rule_remaining){throw 'Driver/rule residue'}
}
Write-Output ('Forward-injection experiment PASS: '+$Evidence)
