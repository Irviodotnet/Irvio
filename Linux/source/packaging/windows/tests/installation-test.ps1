# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Helper,
 [Parameter(Mandatory=$true)][string]$Evidence,
 [switch]$VerifyInstalled
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
. $Helper
if(Test-Path $Evidence){throw 'New evidence required'}
New-Item -ItemType Directory $Evidence|Out-Null
$root=Join-Path $Evidence 'state'
$program=Join-Path $Evidence 'program'
$driver=Join-Path $Evidence 'driver'
function Protect([string]$Path,[bool]$Directory=$false){
 if($Directory){$acl=New-Object Security.AccessControl.DirectorySecurity;$sddl='O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)'}
 else{$acl=New-Object Security.AccessControl.FileSecurity;$sddl='O:BAG:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)'}
 $acl.SetSecurityDescriptorSddlForm($sddl)
 Set-Acl -LiteralPath $Path -AclObject $acl
}
foreach($base in @($root,$program,$driver)){New-Item -ItemType Directory $base|Out-Null;Protect $base $true}
$rows=@()
foreach($path in @('daemon/irviod.exe','daemon/wintun.dll','driver/WinDivert.dll','driver/WinDivert64.sys','ui/irvio.exe','ui/irvio-ui-bridge.exe','geo/country.mmdb')){
 $parts=$path.Split('/',2)
 $base=switch($parts[0]){'daemon'{"$program\daemon"};'ui'{"$program\ui"};'driver'{$driver};'geo'{"$root\geo"}}
 $target=Join-Path $base $parts[1]
 New-Item -ItemType Directory ([IO.Path]::GetDirectoryName($target)) -Force|Out-Null
 [IO.File]::WriteAllText($target,'ownership fixture only, never executable')
 $rows+=@{path=$path;sha256=(Get-FileHash $target).Hash.ToLowerInvariant();size=(Get-Item $target).Length}
}
$manifest=@{schema=1;product='IRVIO';platform='windows-amd64';development_unsigned=$true;files=$rows}
$original=$manifest|ConvertTo-Json -Depth 6
$sid='S-1-5-21-101-202-303-1001'
[IO.File]::WriteAllText("$root\desktop-user.sid",$sid)
Protect "$root\desktop-user.sid"
function Write-Metadata($Value){
 [IO.File]::WriteAllText("$program\payload-manifest.json",($Value|ConvertTo-Json -Depth 6))
 $record=@{schema=1;role='desktop';manifest_sha256=(Get-FileHash "$program\payload-manifest.json").Hash.ToLowerInvariant();desktop_sid=$sid;development_unsigned=$true}
 [IO.File]::WriteAllText("$root\installation.json",($record|ConvertTo-Json))
 Protect "$root\installation.json"
}
Write-Metadata $manifest
$results=New-Object 'System.Collections.Generic.List[object]'
function Reject([string]$Name,[scriptblock]$Action,[string]$Expected){
 $caught=$null
 try{& $Action|Out-Null}catch{$caught=$_.Exception.Message}
 if(-not $caught -or $caught -notmatch $Expected){throw "Wrong rejection for ${Name}: $caught"}
 $results.Add(@{test=$Name;result='PASS';diagnostic=$caught})
}
$installed=Read-IRVIOInstalledFiles $root $program $driver
if($installed.files.Count -ne 7){throw 'Incomplete fixture mapping'}
$results.Add(@{test='valid-file-inventory';result='PASS'})
$read={Read-IRVIOInstalledFiles $root $program $driver}
[IO.File]::AppendAllText("$program\ui\irvio.exe",'changed')
Reject 'modified-file' $read 'Installed file changed'
[IO.File]::WriteAllText("$program\ui\irvio.exe",'ownership fixture only, never executable')
[IO.File]::AppendAllText("$program\payload-manifest.json",' ')
Reject 'modified-manifest' $read 'Installed manifest differs'
Write-Metadata ($original|ConvertFrom-Json)
foreach($bad in @('ui/../outside','ui/a:stream','ui/trailing. /file','ui//double')){
 $modified=$original|ConvertFrom-Json
 $modified.files[0].path=$bad
 Write-Metadata $modified
 Reject "unsafe-$bad" $read 'Unsafe or duplicate'
}
$modified=$original|ConvertFrom-Json
$modified.files=@($modified.files)+@($modified.files[0])
Write-Metadata $modified
Reject 'duplicate' $read 'Unsafe or duplicate'
$modified=$original|ConvertFrom-Json
$modified.files=@($modified.files|Where-Object path -ne 'daemon/irviod.exe')
Write-Metadata $modified
Reject 'missing-daemon' $read 'Incomplete installed payload'
Write-Metadata ($original|ConvertFrom-Json)
[IO.File]::WriteAllText("$root\desktop-user.sid",'S-1-5-21-101-202-303-9999')
Reject 'authorization-mismatch' $read 'Desktop authorization differs'
[IO.File]::WriteAllText("$root\desktop-user.sid",$sid)
$acl=Get-Acl "$program\ui\irvio.exe"
$rule=New-Object Security.AccessControl.FileSystemAccessRule((New-Object Security.Principal.SecurityIdentifier('S-1-1-0')),[Security.AccessControl.FileSystemRights]::Write,[Security.AccessControl.AccessControlType]::Allow)
$acl.AddAccessRule($rule)
Set-Acl "$program\ui\irvio.exe" $acl
Reject 'world-writable-file' $read 'writable by another principal'
Protect "$program\ui\irvio.exe"
Move-Item "$program\ui" "$program\original-ui"
New-Item -ItemType Junction "$program\ui" -Target "$program\original-ui"|Out-Null
try{Reject 'reparse-parent' $read 'Reparse point'}finally{[IO.Directory]::Delete("$program\ui");Move-Item "$program\original-ui" "$program\ui"}
$service=[pscustomobject]@{Name='IRVIO';PathName=('"'+$program+'\daemon\irviod.exe"');StartName='LocalSystem';ServiceType='Own Process'}
$recovery=[pscustomobject]@{Name='IRVIO-Recovery';PathName=($service.PathName+' --boot-recover');StartName='LocalSystem';ServiceType='Own Process'}
$kernel=[pscustomobject]@{Name='WinDivert';PathName=($driver+'\WinDivert64.sys');ServiceType='Kernel Driver';StartMode='Manual'}
$check={Assert-IRVIOInstalledServices $installed $service $recovery $kernel}
& $check
$results.Add(@{test='valid-service-inventory';result='PASS'})
$service.PathName+=' --role server'
Reject 'foreign-service-command' $check 'IRVIO service does not match'
$service.PathName='"'+$program+'\daemon\irviod.exe"'
$service.StartName='LocalService'
Reject 'foreign-service-account' $check 'IRVIO service does not match'
$service.StartName='LocalSystem'
$recovery.PathName=$service.PathName
Reject 'wrong-recovery-command' $check 'Recovery service does not match'
$recovery.PathName=$service.PathName+' --boot-recover'
$kernel.PathName='C:\foreign\WinDivert64.sys'
Reject 'foreign-driver-path' $check 'WinDivert service does not match'
$kernel.PathName=$driver+'\WinDivert64.sys'
$kernel.StartMode='Auto'
Reject 'wrong-driver-startmode' $check 'WinDivert service does not match'
if($VerifyInstalled){
 $actual=Read-IRVIOInstallation
 $results.Add(@{test='real-installed-inventory';result='PASS';files=$actual.files.Count;manifest_sha256=$actual.record.manifest_sha256})
}
$results|ConvertTo-Json -Depth 5|Set-Content "$Evidence\results.json"
Get-FileHash $Helper,$PSCommandPath|ConvertTo-Json|Set-Content "$Evidence\inputs.json"
'INSTALLATION INVENTORY CHECKS PASS (read-only, not uninstall acceptance)'
