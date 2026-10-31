// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

#define WIN32_LEAN_AND_MEAN
#include <winsock2.h>
#include <ws2ipdef.h>
#include <windows.h>
#include <iphlpapi.h>
#include <netioapi.h>
#include <cstdio>
#include <cstddef>
#pragma comment(lib, "iphlpapi.lib")

#define OFF(f) printf("MIB_IPFORWARD_ROW2." #f "=%zu\n", offsetof(MIB_IPFORWARD_ROW2, f))

int main() {
    printf("MIB_IPFORWARD_ROW2=%zu\n",sizeof(MIB_IPFORWARD_ROW2));
    OFF(InterfaceLuid);OFF(InterfaceIndex);OFF(DestinationPrefix);OFF(NextHop);
    OFF(SitePrefixLength);OFF(ValidLifetime);OFF(PreferredLifetime);OFF(Metric);
    OFF(Protocol);OFF(Loopback);OFF(AutoconfigureAddress);OFF(Publish);
    OFF(Immortal);OFF(Age);OFF(Origin);
    MIB_IPFORWARD_ROW2 row{};
    InitializeIpForwardEntry(&row);
    printf("initialized lifetime=%lu preferred=%lu site=%u metric=%lu protocol=%u loopback=%u autoconfigure=%u publish=%u immortal=%u\n",row.ValidLifetime,row.PreferredLifetime,row.SitePrefixLength,row.Metric,row.Protocol,row.Loopback,row.AutoconfigureAddress,row.Publish,row.Immortal);
}
