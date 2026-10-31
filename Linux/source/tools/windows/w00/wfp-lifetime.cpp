// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <initguid.h>
#include <fwpmu.h>
#include <fwpmtypes.h>
#include <cstdio>
#include <cstring>

// This fixed key owns only the narrowly scoped W00 lab filter.
static const GUID probeKey = {0x42b924c3,0xa645,0x4777,{0x9f,0x57,0xc4,0xb6,0x65,0x20,0x91,0xa3}};

int main(int argc, char** argv) {
    if (argc != 2) return 2;
    const bool dynamic = strcmp(argv[1], "dynamic") == 0;
    const bool persistent = strcmp(argv[1], "persistent") == 0;
    const bool remove = strcmp(argv[1], "remove") == 0;
    if (!dynamic && !persistent && !remove && strcmp(argv[1], "static") != 0) return 2;
    FWPM_SESSION0 session = {};
    session.flags = dynamic ? FWPM_SESSION_FLAG_DYNAMIC : 0;
    HANDLE engine = nullptr;
    DWORD error = FwpmEngineOpen0(nullptr, RPC_C_AUTHN_WINNT, nullptr, &session, &engine);
    if (error) { fprintf(stderr,"engine_open=%lu\n",error); return 1; }
    if (remove) {
        error = FwpmFilterDeleteByKey0(engine, &probeKey);
        FwpmEngineClose0(engine);
        if (error && error != FWP_E_FILTER_NOT_FOUND) { fprintf(stderr,"delete=%lu\n",error); return 1; }
        puts("{\"stage\":\"cleanup\",\"result\":\"PASS\"}");
        return 0;
    }
    FWPM_FILTER_CONDITION0 conditions[2] = {};
    conditions[0].fieldKey = FWPM_CONDITION_IP_REMOTE_ADDRESS;
    conditions[0].matchType = FWP_MATCH_EQUAL;
    conditions[0].conditionValue.type = FWP_UINT32;
    conditions[0].conditionValue.uint32 = 0xc0a81702; // Independent mini-PC oracle: 192.168.23.2.
    conditions[1].fieldKey = FWPM_CONDITION_IP_REMOTE_PORT;
    conditions[1].matchType = FWP_MATCH_EQUAL;
    conditions[1].conditionValue.type = FWP_UINT16;
    conditions[1].conditionValue.uint16 = 49494;
    FWPM_FILTER0 filter = {};
    filter.filterKey = probeKey;
    filter.displayData.name = const_cast<wchar_t*>(L"IRVIO W00 lifetime probe");
    filter.layerKey = FWPM_LAYER_ALE_AUTH_CONNECT_V4;
    filter.subLayerKey = FWPM_SUBLAYER_UNIVERSAL;
    filter.weight.type = FWP_EMPTY;
    filter.action.type = FWP_ACTION_BLOCK;
    filter.flags = persistent ? FWPM_FILTER_FLAG_PERSISTENT : 0;
    filter.numFilterConditions = 2;
    filter.filterCondition = conditions;
    UINT64 id = 0;
    error = FwpmFilterAdd0(engine, &filter, nullptr, &id);
    if (error) { FwpmEngineClose0(engine); fprintf(stderr,"filter_add=%lu\n",error); return 1; }
    printf("{\"stage\":\"filter_ready\",\"mode\":\"%s\",\"id\":%llu,\"pid\":%lu}\n",argv[1],id,GetCurrentProcessId());
    fflush(stdout);
    // The harness terminates this process without giving it a cleanup opportunity.
    Sleep(INFINITE);
    return 0;
}
