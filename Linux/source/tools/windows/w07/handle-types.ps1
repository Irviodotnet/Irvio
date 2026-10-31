# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

param([Parameter(Mandatory=$true)][int]$ProcessID)
$ErrorActionPreference='Stop'
if(-not ('IRVIOHandleTypes' -as [type])){
 Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
public static class IRVIOHandleTypes {
 [DllImport("ntdll.dll")] static extern int NtQuerySystemInformation(int kind, IntPtr buffer, int length, out int needed);
 public static Dictionary<int,int> Read(int pid) {
  if(IntPtr.Size!=8) throw new Exception("64-bit lab required");
  int size=1048576;
  for(int attempt=0;attempt<8 && size<=67108864;attempt++) {
   IntPtr data=Marshal.AllocHGlobal(size);
   try {
    int needed; int status=NtQuerySystemInformation(64,data,size,out needed);
    if(status==unchecked((int)0xc0000004)) {size=checked(Math.Max(size*2,needed));continue;}
    if(status!=0) throw new Exception("Handle query status="+status);
    long count=Marshal.ReadInt64(data);
    if(count<0 || count>(size-16)/40) throw new Exception("Invalid handle table bounds");
    var result=new Dictionary<int,int>();
    for(long i=0;i<count;i++) {
     IntPtr row=IntPtr.Add(data,checked(16+(int)i*40));
     if(Marshal.ReadInt64(row,8)!=pid) continue;
     int type=(ushort)Marshal.ReadInt16(row,30);
     result[type]=result.ContainsKey(type)?result[type]+1:1;
    }
    return result;
   } finally {Marshal.FreeHGlobal(data);}
  }
  throw new Exception("Handle table exceeded bounded query budget");
 }
}
'@
}
# Retain only per-type counts for the selected lab process, never object addresses or names.
$result=@{}
foreach($entry in [IRVIOHandleTypes]::Read($ProcessID).GetEnumerator()){$result[[string]$entry.Key]=$entry.Value}
$result
