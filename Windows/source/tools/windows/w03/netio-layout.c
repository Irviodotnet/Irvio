// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

#include <winsock2.h>
#include <windows.h>
#include <netioapi.h>
#include <stddef.h>
#include <stdio.h>

int main(void) {
    printf("MIB_IPINTERFACE_ROW size=%zu metric=%zu mtu=%zu offload=%zu\n",
        sizeof(MIB_IPINTERFACE_ROW), offsetof(MIB_IPINTERFACE_ROW, Metric),
        offsetof(MIB_IPINTERFACE_ROW, NlMtu), offsetof(MIB_IPINTERFACE_ROW, TransmitOffload));
    printf("MIB_IPFORWARD_ROW2 size=%zu table_offset=%zu\n",
        sizeof(MIB_IPFORWARD_ROW2), offsetof(MIB_IPFORWARD_TABLE2, Table));
    printf("MIB_IF_ROW2 size=%zu oper_status=%zu\n", sizeof(MIB_IF_ROW2), offsetof(MIB_IF_ROW2, OperStatus));
    printf("MIB_UNICASTIPADDRESS_ROW size=%zu dad_state=%zu\n", sizeof(MIB_UNICASTIPADDRESS_ROW), offsetof(MIB_UNICASTIPADDRESS_ROW, DadState));
    return 0;
}
