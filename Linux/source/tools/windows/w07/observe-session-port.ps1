# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param(
 [Parameter(Mandatory=$true)][string]$Evidence,
 [ValidateRange(1,3600)][int]$Seconds=2700,
 [ValidateRange(1,65535)][int]$Port=51822
)
$ErrorActionPreference='Stop'
if(Test-Path $Evidence){throw 'New observer evidence required'}
New-Item -ItemType Directory $Evidence | Out-Null
Add-Type -TypeDefinition @'
using System;
using System.Diagnostics;
using System.IO;
using System.Net;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;
public sealed class IRVIOPortObserver : IDisposable {
 [DllImport("iphlpapi.dll")] static extern uint GetExtendedTcpTable(IntPtr table, ref uint size, bool order, uint family, uint kind, uint reserved);
 readonly Thread worker;
 volatile bool stop;
 public string Error;
 public IRVIOPortObserver(string path, int port) {
  worker = new Thread(() => Run(path, port)); worker.IsBackground = true; worker.Start();
 }
 string Read(int port) {
  uint size=0; uint code=GetExtendedTcpTable(IntPtr.Zero,ref size,false,2,5,0);
  if(code!=122 && code!=0) throw new Exception("TCP table size error: "+code);
  for(int attempt=0;attempt<4;attempt++) {
   uint capacity=size; IntPtr table=Marshal.AllocHGlobal(checked((int)capacity));
   try {
    code=GetExtendedTcpTable(table,ref size,false,2,5,0);
    if(code==122) continue;
    if(code!=0) throw new Exception("TCP table read error: "+code);
    int count=Marshal.ReadInt32(table);
    if(count<0 || 4L+24L*count>capacity) throw new Exception("Invalid TCP table bounds");
    var rows=new System.Collections.Generic.List<string>();
    for(int i=0;i<count;i++) {
     IntPtr row=IntPtr.Add(table,4+24*i);
     int localPort=(Marshal.ReadByte(row,8)<<8)|Marshal.ReadByte(row,9);
     if(localPort!=port) continue;
     byte[] ip=new byte[4]; Marshal.Copy(IntPtr.Add(row,4),ip,0,4);
     byte[] remote=new byte[4]; Marshal.Copy(IntPtr.Add(row,12),remote,0,4);
     int remotePort=(Marshal.ReadByte(row,16)<<8)|Marshal.ReadByte(row,17);
     rows.Add(new IPAddress(ip)+":"+localPort+">"+new IPAddress(remote)+":"+remotePort+" state="+Marshal.ReadInt32(row)+" pid="+Marshal.ReadInt32(row,20));
    }
    rows.Sort(StringComparer.Ordinal); return String.Join(";",rows);
   } finally { Marshal.FreeHGlobal(table); }
  }
  throw new Exception("TCP table repeatedly resized");
 }
 void Run(string path,int port) {
  try {
   using(var output=new StreamWriter(path,false,Encoding.UTF8)) {
    output.AutoFlush=true; var clock=Stopwatch.StartNew(); string previous=null;
    while(!stop) {
     string current=Read(port);
     if(current!=previous) { output.WriteLine(DateTime.UtcNow.ToString("o")+"\t"+clock.ElapsedMilliseconds+"\t"+current); previous=current; }
     Thread.Sleep(25);
    }
    output.WriteLine(DateTime.UtcNow.ToString("o")+"\t"+clock.ElapsedMilliseconds+"\tOBSERVER_STOPPED");
   }
  } catch(Exception e) { Error=e.ToString(); }
 }
 public void Dispose() { stop=true; if(!worker.Join(5000)) throw new Exception("TCP observer did not stop"); }
}
'@
$start=Get-Date
$null=Get-WinEvent -ListLog Security
$clock=[Diagnostics.Stopwatch]::StartNew()
$sampler=New-Object IRVIOPortObserver("$Evidence\tcp-transitions.tsv",$Port)
$lastRecord=0L
$lastExclusions=''
$nextExclusions=0L
$eventCount=0
try {
 'READY' | Set-Content "$Evidence\ready.txt"
 while($clock.Elapsed.TotalSeconds -lt $Seconds -and -not(Test-Path "$Evidence\stop")) {
  if($sampler.Error){throw $sampler.Error}
  $xpath="*[System[(EventID=5159 or EventID=5155) and EventRecordID > $lastRecord] and EventData[Data[@Name='SourcePort']='$Port']]"
  $eventErrors=@()
  $events=@(Get-WinEvent -LogName Security -FilterXPath $xpath -MaxEvents 100 -ErrorAction SilentlyContinue -ErrorVariable eventErrors | Where-Object {$_.TimeCreated -ge $start} | Sort-Object RecordId)
  foreach($readError in $eventErrors){if($readError.FullyQualifiedErrorId -notlike 'NoMatchingEventsFound*'){throw $readError}}
  foreach($event in $events){
   $event.ToXml() | Set-Content "$Evidence\blocked-$($event.RecordId).xml"
   $lastRecord=$event.RecordId
   $eventCount++
   if($eventCount -le 5){
    & netsh.exe wfp show filters "file=$Evidence\filters-$lastRecord.xml" > "$Evidence\filters-$lastRecord.log"
    & netsh.exe interface ipv4 show excludedportrange protocol=tcp > "$Evidence\exclusions-event-$lastRecord.txt"
   }
  }
  if($clock.ElapsedMilliseconds -ge $nextExclusions){
   $ranges=(& netsh.exe interface ipv4 show excludedportrange protocol=tcp | Out-String)
   if($LASTEXITCODE){throw 'Cannot observe port exclusions'}
   if($ranges -ne $lastExclusions){
    [DateTime]::UtcNow.ToString('o') | Add-Content "$Evidence\exclusions.txt"
    $ranges | Add-Content "$Evidence\exclusions.txt"
    $lastExclusions=$ranges
   }
   $nextExclusions=$clock.ElapsedMilliseconds+2000
  }
  Start-Sleep -Milliseconds 250
 }
} finally {
 $sampler.Dispose()
 @{elapsed_ms=$clock.ElapsedMilliseconds;blocked_events=$eventCount;tcp_error=$sampler.Error;sampling_limit='IPv4 TCP snapshot every 25 ms; short-lived sockets may be missed; no bound-only table';audit_limit='Requires caller-enabled failure auditing; missing events do not rule out bind denial';exclusions_limit='Sampled every 2 s; no proof of state between samples'} | ConvertTo-Json | Set-Content "$Evidence\observer-result.json"
}
if($sampler.Error){throw $sampler.Error}
