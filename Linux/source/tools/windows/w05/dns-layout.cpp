// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <windows.h>
#include <iphlpapi.h>
#include <netioapi.h>
#include <cstdio>
#include <cstddef>
#include <vector>
#include <cstring>
#pragma comment(lib, "iphlpapi.lib")

#define OFF(f) printf("DNS_INTERFACE_SETTINGS." #f "=%zu\n", offsetof(DNS_INTERFACE_SETTINGS, f))

static void show(GUID id, ULONG64 flags) {
    DNS_INTERFACE_SETTINGS s{};
    s.Version=DNS_INTERFACE_SETTINGS_VERSION1;s.Flags=flags;
    auto result=GetInterfaceDnsSettings(id,&s);
    printf("read flags_in=%llu result=%lu servers=%ls flags_out=%llu\n",flags,result,s.NameServer?s.NameServer:L"<null>",s.Flags);
    if(result==NO_ERROR) FreeInterfaceDnsSettings(&s);
}

int main(int argc,char**argv) {
    printf("DNS_INTERFACE_SETTINGS=%zu\n", sizeof(DNS_INTERFACE_SETTINGS));
    OFF(Version); OFF(Flags); OFF(Domain); OFF(NameServer); OFF(SearchList);
    OFF(RegistrationEnabled); OFF(RegisterAdapterName); OFF(EnableLLMNR);
    OFF(QueryAdapterName); OFF(ProfileNameServer);
    printf("DNS_INTERFACE_SETTINGS3=%zu supplemental=%zu properties_count=%zu properties=%zu profile_count=%zu profile_properties=%zu\n",sizeof(DNS_INTERFACE_SETTINGS3),offsetof(DNS_INTERFACE_SETTINGS3,SupplementalSearchList),offsetof(DNS_INTERFACE_SETTINGS3,cServerProperties),offsetof(DNS_INTERFACE_SETTINGS3,ServerProperties),offsetof(DNS_INTERFACE_SETTINGS3,cProfileServerProperties),offsetof(DNS_INTERFACE_SETTINGS3,ProfileServerProperties));
    ULONG size=32768;
    std::vector<BYTE> buf(size);
    auto adapters=reinterpret_cast<IP_ADAPTER_ADDRESSES*>(buf.data());
    auto result=GetAdaptersAddresses(AF_UNSPEC, GAA_FLAG_INCLUDE_ALL_INTERFACES, nullptr, adapters, &size);
    if(result!=NO_ERROR) { printf("adapters_error=%lu\n",result); return 1; }
    for(auto a=adapters;a;a=a->Next) {
        GUID id;
        result=ConvertInterfaceLuidToGuid(&a->Luid,&id);
        if(result!=NO_ERROR) return 2;
        DNS_INTERFACE_SETTINGS s{};
        s.Version=DNS_INTERFACE_SETTINGS_VERSION1;
        result=GetInterfaceDnsSettings(id,&s);
        printf("adapter=%ls luid=%llu result=%lu flags=%llx\n",a->FriendlyName,a->Luid.Value,result,s.Flags);
        if(result==NO_ERROR) {
            printf(" domain=%ls servers=%ls search=%ls profile=%ls registration=%lu adapter=%lu llmnr=%lu query=%lu\n",s.Domain?s.Domain:L"<null>",s.NameServer?s.NameServer:L"<null>",s.SearchList?s.SearchList:L"<null>",s.ProfileNameServer?s.ProfileNameServer:L"<null>",s.RegistrationEnabled,s.RegisterAdapterName,s.EnableLLMNR,s.QueryAdapterName);
            FreeInterfaceDnsSettings(&s);
        }
        if(argc==2 && strcmp(argv[1],"owned-lab-change")==0 && wcscmp(a->FriendlyName,L"IRVIO-DNS")==0) {
            DNS_INTERFACE_SETTINGS v4{},v6{};
            v4.Version=v6.Version=DNS_INTERFACE_SETTINGS_VERSION1;
            v4.Flags=DNS_SETTING_NAMESERVER;v6.Flags=DNS_SETTING_NAMESERVER|DNS_SETTING_IPV6;
            v4.NameServer=const_cast<PWSTR>(L"192.0.2.53");v6.NameServer=const_cast<PWSTR>(L"2001:db8::53");
            printf("set v4=%lu\n",SetInterfaceDnsSettings(id,&v4));show(id,0);show(id,DNS_SETTING_IPV6);
            printf("set v6=%lu\n",SetInterfaceDnsSettings(id,&v6));show(id,0);show(id,DNS_SETTING_IPV6);
            v4.NameServer=v6.NameServer=const_cast<PWSTR>(L"");
            printf("reset v4=%lu v6=%lu\n",SetInterfaceDnsSettings(id,&v4),SetInterfaceDnsSettings(id,&v6));show(id,0);show(id,DNS_SETTING_IPV6);
        }
    }
}
