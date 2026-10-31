# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

function Read-IRVIOSetupManifest([string]$Executable) {
 if(-not ('IRVIOSetup.Manifest' -as [type])){
  Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;
namespace IRVIOSetup {
 public static class Manifest {
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern IntPtr LoadLibraryEx(string file, IntPtr reserved, uint flags);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern IntPtr FindResource(IntPtr module, IntPtr name, IntPtr type);
  [DllImport("kernel32.dll", SetLastError=true)] static extern uint SizeofResource(IntPtr module, IntPtr resource);
  [DllImport("kernel32.dll", SetLastError=true)] static extern IntPtr LoadResource(IntPtr module, IntPtr resource);
  [DllImport("kernel32.dll")] static extern IntPtr LockResource(IntPtr resource);
  [DllImport("kernel32.dll")] static extern bool FreeLibrary(IntPtr module);
  public static string Read(string path) {
   IntPtr module=LoadLibraryEx(path,IntPtr.Zero,0x22);
   if(module==IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
   try {
    IntPtr resource=FindResource(module,new IntPtr(1),new IntPtr(24));
    if(resource==IntPtr.Zero) throw new Exception("Installer manifest resource is missing");
    uint size=SizeofResource(module,resource);
    if(size==0 || size>65536) throw new Exception("Invalid installer manifest size");
    IntPtr data=LockResource(LoadResource(module,resource));
    if(data==IntPtr.Zero) throw new Exception("Installer manifest resource cannot be read");
    byte[] bytes=new byte[size]; Marshal.Copy(data,bytes,0,bytes.Length);
    return new UTF8Encoding(false,true).GetString(bytes).TrimEnd('\0');
   } finally {FreeLibrary(module);}
  }
 }
}
'@
 }
 $text=[IRVIOSetup.Manifest]::Read([IO.Path]::GetFullPath($Executable))
 $xml=New-Object Xml.XmlDocument
 $xml.XmlResolver=$null
 $xml.LoadXml($text)
 $levels=$xml.GetElementsByTagName('requestedExecutionLevel','urn:schemas-microsoft-com:asm.v3')
 if($levels.Count -ne 1 -or $levels[0].GetAttribute('level') -cne 'asInvoker' -or $levels[0].GetAttribute('uiAccess') -cne 'false'){throw 'Installer must capture desktop identity before elevation: asInvoker/uiAccess=false required'}
 return $text
}
