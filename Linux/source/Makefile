# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

.PHONY: proto build test test-race test-m3a test-m3b test-m4a test-m5a \
	test-m5a-netns test-m5a-soak test-regression test-m4b-build test-m4b \
	test-m4b-soak test-m5b test-m5b-netns test-m5b-soak test-m5c1 \
	test-m5c1-netns test-m5c1-soak test-m5c2 test-m5c2-netns \
test-m5c2-soak test-m5c3 test-m5c3-netns test-m5c3-soak \
test-m6 test-m6-netns test-m6-soak test-m7 test-m7-netns test-m7-soak \
test-m8 test-m8-netns test-m8-soak test-m9 test-m9-netns test-m9-soak \
test-m10 test-m10-netns test-m10-soak test-m11 test-m11-netns test-m11-soak \
	test-m12 test-m12-netns test-m12-soak test-m12-scale demo-m12 test-p1 test-p1-netns test-p1-soak demo-p1 \
	test-p2 test-p2-netns test-p2-soak demo-p2 clean-m4b demo clean

PROTOC_GEN_GO ?= $(shell go env GOPATH)/bin/protoc-gen-go
M4B_NATPROBE ?= /tmp/hydra-m4b-natprobe
M4B_RFC5780 ?= /tmp/hydra-m4b-rfc5780
M4B_SOCKETMUX ?= /tmp/hydra-m4b-socketmux
M4B_HYDRAD ?= /tmp/hydra-m4b-hydrad
M4B_STUNTEST ?= /tmp/hydra-m4b-stunprobe.test
M4B_SOCKETTEST ?= /tmp/hydra-m4b-socketmux.test
M4B_RUNNER ?= /tmp/hydra-m4b-privileged.test
M4B_CONNTRACK ?= /tmp/hydra-m4b-conntrack
M5A_ICEHELPER ?= /tmp/hydra-m5a-iceadapter
M5B_SIGNALHELPER ?= /tmp/hydra-m5b-icesignal
M5C3_NETWORKSTATE ?= /tmp/hydra-m5c3-networkstate
M7_INTERNET ?= /tmp/hydra-m7-internet
M8_KILLSWITCH ?= /tmp/hydra-m8-killswitch
M4B_LAB_DNS_LAUNCHER ?= /tmp/hydra-m4b-lab-dns-launcher
FLUTTER ?= flutter
DART ?= dart
P2_UI_CLIENT ?= /tmp/hydra-p2-local-api-client

proto:
	PATH="$(dir $(PROTOC_GEN_GO)):$$PATH" protoc --go_out=. --go_opt=paths=source_relative \
		proto/irvio/v1/node.proto proto/irvio/v1/ice.proto proto/irvio/v1/tunnel.proto \
		proto/irvio/v1/reachability.proto proto/irvio/v1/rendezvous.proto \
		proto/irvio/v1/private_rendezvous.proto \
		proto/irvio/v1/session_establish.proto proto/irvio/v1/session_control.proto \
		proto/irvio/local/v1/control.proto

build:
	go build -o bin/irviod ./cmd/irviod

test:
	go test ./...

test-race:
	go test -race ./...

test-m3a:
	go test -count=1 ./internal/socketmux/...
	go test -count=1 ./test/integration -run '^TestM3A'

test-m3b:
	go test -count=1 ./internal/stunprobe/...
	go test -count=1 ./test/integration -run '^TestM3B'
	go test -count=1 ./internal/socketmux/...

test-m4a:
	go test -count=1 ./internal/natprobe/...
	go test -count=1 ./internal/stunprobe/...
	go test -count=1 ./test/integration -run '^TestM4A'
	go test -count=1 ./test/integration -run '^TestM3[AB]'
	go test -count=1 ./internal/socketmux/...

test-m5a:
	go test -count=1 ./internal/iceadapter/...
	go test -count=1 ./internal/socketmux/...
	$(MAKE) test-m5a-netns

test-regression:
	gofmt -l .
	go vet ./...
	go test -count=1 ./...
	go test -race -count=1 ./...
	$(MAKE) test-m3a
	$(MAKE) test-m3b
	$(MAKE) test-m4a

test-m4b-build:
	go build -trimpath -o $(M4B_NATPROBE) ./test/cmd/irvio-test-natprobe
	go build -trimpath -o $(M4B_RFC5780) ./test/cmd/irvio-test-rfc5780
	go build -trimpath -o $(M4B_SOCKETMUX) ./test/cmd/irvio-test-socketmux
	go build -trimpath -o $(M4B_HYDRAD) ./cmd/irviod
	go test -c -o $(M4B_STUNTEST) ./internal/stunprobe
	go test -c -o $(M4B_SOCKETTEST) ./internal/socketmux
	go build -trimpath -o $(M5A_ICEHELPER) ./test/cmd/irvio-test-iceadapter
	go build -trimpath -o $(M5B_SIGNALHELPER) ./test/cmd/irvio-test-icesignal
	go build -trimpath -o $(M5C3_NETWORKSTATE) ./test/cmd/irvio-test-networkstate
	go build -trimpath -o $(M7_INTERNET) ./test/cmd/irvio-test-internet
	go build -trimpath -o $(M8_KILLSWITCH) ./test/cmd/irvio-test-killswitch
	go build -trimpath -o $(M4B_LAB_DNS_LAUNCHER) ./test/cmd/irvio-test-lab-dns-launcher
	go build -trimpath -o $(M4B_CONNTRACK) ./test/cmd/irvio-test-conntrack
	go test -c -o $(M4B_RUNNER) ./test/netns/m4b

test-m4b: test-m4b-build
	@command -v sudo >/dev/null || { echo "M4b FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M4b FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_NATPROBE=$(M4B_NATPROBE) \
		HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M4B_SOCKETMUX=$(M4B_SOCKETMUX) HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_STUNTEST=$(M4B_STUNTEST) HYDRA_M4B_SOCKETTEST=$(M4B_SOCKETTEST) HYDRA_M4B_CONNTRACK=$(M4B_CONNTRACK) HYDRA_M5A_ICEHELPER=$(M5A_ICEHELPER) \
	HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M5C3_NETWORKSTATE=$(M5C3_NETWORKSTATE) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
	$(M4B_RUNNER) -test.v -test.count=1 -test.timeout=20m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_NATPROBE=$(M4B_NATPROBE) \
		HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M4B_SOCKETMUX=$(M4B_SOCKETMUX) HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_STUNTEST=$(M4B_STUNTEST) HYDRA_M4B_SOCKETTEST=$(M4B_SOCKETTEST) HYDRA_M5A_ICEHELPER=$(M5A_ICEHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM4B_ControlledNATMatrix$$' -test.count=2 -test.timeout=20m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_NATPROBE=$(M4B_NATPROBE) \
		HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M4B_SOCKETMUX=$(M4B_SOCKETMUX) HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_STUNTEST=$(M4B_STUNTEST) HYDRA_M4B_SOCKETTEST=$(M4B_SOCKETTEST) HYDRA_M5A_ICEHELPER=$(M5A_ICEHELPER) \
		$(M4B_RUNNER) -test.v \
		-test.run '^TestM4B_(DirectDualStack|SingleNATNativeObservation|IPv6FilteringMatrix)$$' \
		-test.count=10 -test.timeout=30m

test-m5c3:
	go test -count=1 ./internal/networkstate/... ./internal/iceengine/... ./internal/transportgen/... ./internal/connsupervisor/... ./internal/iceconnect/...
	go test -count=1 ./test/integration -run '^TestM5C3_'

test-m5c3-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M5c3 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5c3 FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5C3_NETWORKSTATE=$(M5C3_NETWORKSTATE) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C3_' -test.count=1 -test.timeout=10m

test-m5c3-soak: test-m4b-build
	HYDRA_M5C3_SOAK_ITERATIONS=100 go test -race -count=1 ./test/integration -run '^TestM5C3_ExplicitReconnectSoak$$'
	go test -race -count=25 ./internal/iceengine/... -run '^TestEngineReconnectsOneOfThreeClientsWithoutDisturbingOthers$$'
	go test -race -count=25 ./test/integration -run '^TestM5C3_FullTransportGenerationMigration$$'
	@command -v sudo >/dev/null || { echo "M5c3 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5c3 soak FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5C3_NETWORKSTATE=$(M5C3_NETWORKSTATE) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C3_RealNetlinkNetworkGenerations$$' -test.count=25 -test.timeout=15m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C3_(PersistentFailureCreatesFreshSessionGeneration|PersistentIPv6FailureCreatesFreshSessionGeneration|EIMAPDFNATReconnectUsesFreshICESessionOnSameSocket)$$' -test.count=10 -test.timeout=15m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C3_DualStackSelectedFamilyLossRecoversOrRestarts$$' -test.count=10 -test.timeout=10m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C3_RealNetlink(NATTransportMigration|IPv4AddressMigration|IPv6AddressMigration)$$' -test.count=10 -test.timeout=20m

test-m6:
	go test -count=1 ./internal/awg/... ./internal/transportgen/...
	go test -count=1 ./test/integration -run '^TestM6_'
	$(MAKE) test-m6-netns

test-m6-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M6 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M6 FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM6_' -test.count=1 -test.timeout=10m

test-m6-soak: test-m4b-build
	go test -race -count=100 ./internal/awg -run '^TestRealAmneziaWGBidirectionalInnerTraffic/outer-udp4-inner-ipv4$$'
	go test -race -count=25 ./internal/awg -run '^TestM6_OneExitTransportServesThreeEncryptedPeersAndIsolatesRestart$$'
	go test -race -count=25 ./test/integration -run '^TestM6_SameNetworkFreshICESessionUpdatesAWGEndpoint$$'
	go test -race -count=10 ./test/integration -run '^TestM6_FullTransportGenerationMigrationEncryptedDataPlane$$'
	@command -v sudo >/dev/null || { echo "M6 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M6 soak FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM6_DirectEncryptedDataPlane$$' -test.count=10 -test.timeout=20m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
$(M4B_RUNNER) -test.v -test.run '^TestM6_ControlledNATEncryptedDataPlane$$' -test.count=10 -test.timeout=30m

test-m7:
	go test -count=1 ./internal/tunnel/... ./internal/tunnellease/... ./internal/tunnelsignal/...
	go test -count=1 ./internal/linuxroute/... ./internal/exitfirewall/... ./internal/ipforward/... ./internal/tunnelsession/...
	$(MAKE) test-m7-netns

test-m7-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M7 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M7 FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) \
	$(M4B_RUNNER) -test.v -test.run '^TestM7_' -test.count=1 -test.timeout=15m

test-m7-soak: test-m4b-build
	go test -race -count=100 ./internal/tunnellease -run '^TestAllocatorIssuesUniqueDualStackLeasesAndRejectsWrongPeer$$'
	go test -race -count=50 ./internal/linuxroute -run '^TestApplyAndCloseOwnExactState$$'
	go test -race -count=25 ./internal/tunnelsession -run '^TestSessionKeepsTUNAcrossGenerationSwap$$'
	@command -v sudo >/dev/null || { echo "M7 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M7 soak FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) \
	$(M4B_RUNNER) -test.v -test.run '^TestM7_EncryptedInternetExit$$' -test.count=20 -test.timeout=30m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) \
	$(M4B_RUNNER) -test.v -test.run '^TestM7_ThreeClientsOneExitInternetIsolation$$' -test.count=25 -test.timeout=30m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) \
	$(M4B_RUNNER) -test.v -test.run '^TestM7_SamePeerReconnectPreservesLogicalTunnel$$' -test.count=10 -test.timeout=30m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) \
	$(M4B_RUNNER) -test.v -test.run '^TestM7_NetworkGenerationMigrationPreservesLogicalTunnel$$' -test.count=10 -test.timeout=30m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) \
	$(M4B_RUNNER) -test.v -test.run '^TestM7_EIMAPDFNATInternetExit$$' -test.count=10 -test.timeout=30m

test-m8:
	go test -count=1 ./internal/killswitch/... ./internal/controlsocket/... ./internal/discovery/...
	$(MAKE) test-m8-netns

test-m8-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M8 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M8 FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM8_' -test.count=1 -test.timeout=20m

test-m8-soak: test-m4b-build
	go test -race -count=100 ./internal/killswitch -run '^TestControllerProtectsBeforeTunnelStatesAndRemovesLast$$'
	go test -race -count=50 ./internal/killswitch -run '^TestControllerAdoptsPersistentProtection$$'
	@command -v sudo >/dev/null || { echo "M8 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M8 soak FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM8_KernelPolicyLifecycle$$' -test.count=50 -test.timeout=30m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM8_CrashAdoptionAndExplicitDisconnect$$' -test.count=10 -test.timeout=20m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM8_ProtectedModeKernelPolicy$$' -test.count=10 -test.timeout=30m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM8_HealthyVPNAndExitDeathRemainFailClosed$$' -test.count=25 -test.timeout=45m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM8_SamePeerReconnectGapRemainsFailClosed$$' -test.count=25 -test.timeout=45m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM8_NetworkGenerationMigrationRemainsFailClosed$$' -test.count=10 -test.timeout=30m

test-m9:
	go test -count=1 ./internal/exitselector/... ./internal/tunnellease/... ./internal/tunnelsignal/...
	$(MAKE) test-m9-netns

test-m9-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M9 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M9 FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM9_' -test.count=1 -test.timeout=10m

test-m9-soak: test-m4b-build
	go test -race -count=100 ./internal/exitselector -run '^TestSelector(InitialSelectionAndHealthyActiveIsSticky|TransientActiveRecoveryDoesNotConsumeReserve)$$'
	go test -race -count=50 ./internal/exitselector -run '^TestSelector(PersistentFailurePromotesReserve|DeadReservePromotesColdWithoutRecoveryWindow)$$'
	go test -race -count=25 ./internal/exitselector -run '^TestSelector(ActiveFailureReserveBusyThenCold|ExhaustionImmediatelyRequestsFreshBatch|ProviderFailureIsIndependentFromHealthyActive|UnderlayOutagePausesUsableRecoveryBudget|UnderlayFlappingDoesNotConsumeFallbacks)$$'
	go test -race -count=25 ./internal/exitselector -run '^Test(EvaluateBatchProbeFanoutIsBoundedAndCancellationStopsStorm|JitteredRefreshIsMeaningfullyDistributed)$$'
	go test -race -count=10 ./internal/tunnellease -run '^TestAllocatorAbandonedLeaseExpiresAndSameClientCanReturn$$'
	@command -v sudo >/dev/null || { echo "M9 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M9 soak FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM9_CandidateProbeUsesM8BypassWithoutActiveExit$$' -test.count=10 -test.timeout=10m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM9_FullStackPersistentActiveFailureSelectsReserve$$' -test.count=20 -test.timeout=15m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM9_FullStackDeadReserveColdAndFreshBatch$$' -test.count=10 -test.timeout=15m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM9_FullStackUnderlayOutagePreservesActiveExit$$' -test.count=10 -test.timeout=15m

test-m10:
	go test -count=1 ./internal/awgidentity/... ./internal/awgbinding/... ./internal/underlay/...
	go test -count=1 ./internal/tunnellease/... ./internal/tunnelsignal/... ./internal/runtime/...
	$(MAKE) test-m10-netns

test-m10-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M10 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M10 FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM10_' -test.count=1 -test.timeout=10m

test-m10-soak: test-m4b-build
	go test -race -count=100 ./internal/runtime -run '^TestRuntimeSerializesProtectionSelectionAndDisconnect$$'
	go test -race -count=100 ./internal/tunnellease -run '^TestLifecycle(RenewalKeepsLeaseAndAdvancesGeneration|ExpiryRevokesBeforeAddressReuse)$$'
	go test -race -count=100 ./internal/tunnellease -run '^TestRenewerKeepsLeaseIdentityAcrossMultipleTTLs$$'
	go test -race -count=50 ./internal/tunnellease -run '^TestLifecycleRenewVsExpiryHasOneWinnerAndIsolatesClients$$'
	go test -race -count=20 ./internal/awgbinding ./internal/underlay
	@command -v sudo >/dev/null || { echo "M10 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M10 soak FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM10_HydradOwnsFullProductionInternetLifecycle$$' -test.count=25 -test.timeout=30m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM10_HydradAutomaticExitFailoverRemainsProtected$$' -test.count=25 -test.timeout=30m

test-m11:
	go test -count=1 ./internal/reachability/... ./internal/controlbypass/...
	go test -count=1 ./internal/linuxroute/... ./internal/tunnel/... ./internal/runtime/... ./internal/discovery/... ./internal/protocol/...
	$(MAKE) test-m11-netns

test-m11-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M11 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M11 FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM11_' -test.count=1 -test.timeout=20m

test-m11-soak: test-m4b-build
	go test -race -count=100 ./internal/reachability -run '^TestController'
	go test -race -count=100 ./internal/reachability -run '^Test(ProofRejects|AttemptAndNonceReplay|AttestationExpiry)'
	go test -race -count=50 ./internal/runtime -run '^TestPublicRolePublishesOnlyQuorumVerifiedEndpointAndDisableClearsIt$$'
	@command -v sudo >/dev/null || { echo "M11 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M11 soak FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM11_StaticPATQuorumAndAcceptedControlBypass$$' -test.count=50 -test.timeout=60m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM11_HydradDualRolePublicControlAndNoImplicitMultiHop$$' -test.count=25 -test.timeout=45m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM11_IPv6PublicVerificationAndNetworkMigration$$' -test.count=10 -test.timeout=20m

test-m12:
	go test -count=1 ./internal/rendezvous/... ./internal/sessionestablish/... ./internal/sessioncontrol/... ./internal/runtime/... ./internal/awg/...
	$(MAKE) test-m12-netns

test-m12-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M12 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M12 FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M4B_CONNTRACK=$(M4B_CONNTRACK) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM12_' -test.count=1 -test.timeout=20m

test-m12-soak: test-m4b-build
	go test -race -count=100 ./internal/rendezvous -run '^Test(RegistrationReplacementFencesLateOwnerEvents|RegistrationStaleReviveAndExpiry|HeartbeatRenewsReceiptBeforeExpiry)$$'
	go test -race -count=100 ./internal/sessionestablish -run '^Test(SignedTranscriptAuthenticatesExactBytesAndChain|ReplayGuardCoalescesExactDuplicateAndRejectsMutation)$$'
	go test -race -count=100 ./internal/sessioncontrol -run '^Test(NoiseSessionControlBindsRenewsRebindsAndReleases|NoiseSessionControlCommitsExactAWGRotationOnce|ReconnectingClientRepairsBrokenNoiseChannelWithoutBroker|SessionControlReplayIsIdempotentBeforeRevisionCheck)$$'
	go test -race -count=25 ./internal/awg -run '^TestTransportRotatesKeysWithoutReplacingMux$$'
	go test -race -count=50 ./internal/rendezvous -run '^Test(CandidateProviderProgressesFiveToTenWithinFourGETs|CandidateProviderSignalsThroughCandidateSourceFallback|TwoBrokerSamplingAndSignalingDoesNotRecurse|BrokerRoutesMultipleExitsOwnedByOneRendezvous)$$'
	@command -v sudo >/dev/null || { echo "M12 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M12 soak FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM12_HydradBrokeredInternetAndRendezvousIndependentRenewal$$' -test.count=25 -test.timeout=60m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM12_HydradBrokeredExitFailoverRemainsProtected$$' -test.count=10 -test.timeout=30m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM12_HydradDualRoleMaintainsIndependentTunnelControl$$' -test.count=10 -test.timeout=30m

test-m12-scale:
	go test -count=1 ./internal/rendezvous/scalemodel
	HYDRA_M12_LOAD=1 go test -v -count=1 ./internal/rendezvous -run '^TestM12AuthenticatedRendezvousLoad$$' -timeout=2m
	go run ./test/cmd/irvio-test-m12-scale -exits=1000000 -clients=1000000 -servers=1000 -seed=2037407826

demo-m12: test-m4b-build
	@command -v sudo >/dev/null || { echo "M12 demo FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M12 demo FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestM12_HydradBrokeredInternetAndRendezvousIndependentRenewal$$' -test.count=1 -test.timeout=20m

test-p1:
	go test -count=1 ./internal/localapi/...
	$(MAKE) test-p1-netns

test-p1-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "P1 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "P1 FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestP1_' -test.count=1 -test.timeout=30m

test-p1-soak: test-m4b-build
	go test -race -count=500 ./internal/localapi -run '^TestGetStatusAndCommandIdempotence$$'
	go test -race -count=200 ./internal/localapi -run '^TestEventsAndGUIReconnectDoNotOwnCoreLifecycle$$'
	go test -race -count=100 ./internal/localapi -run '^Test(ReadFrameBoundsAndMalformedInput|SlowSubscriberIsDisconnectedWithoutBlockingController)$$'
	@command -v sudo >/dev/null || { echo "P1 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "P1 soak FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestP1_HydradLocalAPIControlsFrozenM12Runtime$$' -test.count=50 -test.timeout=90m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestP1_CoreFailoverIsObservedWithoutLocalOwnership$$' -test.count=25 -test.timeout=60m

demo-p1: test-m4b-build
	@command -v sudo >/dev/null || { echo "P1 demo FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "P1 demo FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) \
		$(M4B_RUNNER) -test.v -test.run '^TestP1_HydradLocalAPIControlsFrozenM12Runtime$$' -test.count=1 -test.timeout=30m

test-p2:
	cd ui && NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost $(FLUTTER) analyze
	cd ui && NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost $(FLUTTER) test
	cd ui && $(FLUTTER) build linux --release

test-p2-netns: test-m4b-build
	cd ui && $(DART) compile exe bin/local_api_smoke.dart -o $(P2_UI_CLIENT)
	@command -v sudo >/dev/null || { echo "P2 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "P2 FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) HYDRA_P2_UI_CLIENT=$(P2_UI_CLIENT) \
		$(M4B_RUNNER) -test.v -test.run '^TestP2_' -test.count=1 -test.timeout=60m

test-p2-soak: test-m4b-build
	cd ui && NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost $(FLUTTER) test test/p2_soak_test.dart --reporter compact
	go test -race -count=100 ./internal/localapi -run '^TestSlowSubscriberIsDisconnectedWithoutBlockingController$$'
	cd ui && $(DART) compile exe bin/local_api_smoke.dart -o $(P2_UI_CLIENT)
	@command -v sudo >/dev/null || { echo "P2 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "P2 soak FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) HYDRA_P2_UI_CLIENT=$(P2_UI_CLIENT) \
		$(M4B_RUNNER) -test.v -test.run '^TestP2_HydraDartClientControlsFrozenM12Runtime$$' -test.count=50 -test.timeout=90m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M7_INTERNET=$(M7_INTERNET) HYDRA_M8_KILLSWITCH=$(M8_KILLSWITCH) HYDRA_P2_UI_CLIENT=$(P2_UI_CLIENT) \
		$(M4B_RUNNER) -test.v -test.run '^TestP2_HydraDartClientObservesCoreFailover$$' -test.count=25 -test.timeout=90m

demo-p2: test-p2-netns

test-m4b-soak: test-m4b-build
	@command -v sudo >/dev/null || { echo "M4b soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M4b soak FAIL: passwordless sudo/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_NATPROBE=$(M4B_NATPROBE) \
		HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M4B_SOCKETMUX=$(M4B_SOCKETMUX) HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_STUNTEST=$(M4B_STUNTEST) HYDRA_M4B_SOCKETTEST=$(M4B_SOCKETTEST) HYDRA_M5A_ICEHELPER=$(M5A_ICEHELPER) \
		$(M4B_RUNNER) -test.v \
		-test.run '^TestM4B_(DirectDualStack|SingleNATNativeObservation|DiscoveryAcrossNamespaces)$$' \
		-test.count=5 -test.timeout=30m

test-m5a-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M5a FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5a FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_NATPROBE=$(M4B_NATPROBE) \
		HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M4B_SOCKETMUX=$(M4B_SOCKETMUX) HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_STUNTEST=$(M4B_STUNTEST) HYDRA_M4B_SOCKETTEST=$(M4B_SOCKETTEST) HYDRA_M5A_ICEHELPER=$(M5A_ICEHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5A_' -test.count=1 -test.timeout=5m

test-m5a-soak: test-m4b-build
	go test -race -count=25 ./internal/iceadapter/... -run '^TestM5A_PionAgentHostCandidatesUseHydraMux$$'
	@command -v sudo >/dev/null || { echo "M5a soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5a soak FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_NATPROBE=$(M4B_NATPROBE) \
		HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M4B_SOCKETMUX=$(M4B_SOCKETMUX) HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_STUNTEST=$(M4B_STUNTEST) HYDRA_M4B_SOCKETTEST=$(M4B_SOCKETTEST) HYDRA_M5A_ICEHELPER=$(M5A_ICEHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5A_' -test.count=5 -test.timeout=10m

test-m5b:
	go test -count=1 ./internal/icesession/... ./internal/icesignal/...
	go test -count=1 ./test/integration -run '^TestM5B_'

test-m5b-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M5b FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5b FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_NATPROBE=$(M4B_NATPROBE) \
		HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M4B_SOCKETMUX=$(M4B_SOCKETMUX) HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_STUNTEST=$(M4B_STUNTEST) HYDRA_M4B_SOCKETTEST=$(M4B_SOCKETTEST) HYDRA_M5A_ICEHELPER=$(M5A_ICEHELPER) \
		HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5B_' -test.count=1 -test.timeout=10m

test-m5b-soak: test-m4b-build
	go test -race -count=100 ./test/integration -run '^TestM5B_AuthenticatedDualStackDescriptionExchange$$'
	@command -v sudo >/dev/null || { echo "M5b soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5b soak FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) HYDRA_M4B_NATPROBE=$(M4B_NATPROBE) HYDRA_M4B_RFC5780=$(M4B_RFC5780) \
		HYDRA_M4B_SOCKETMUX=$(M4B_SOCKETMUX) HYDRA_M4B_HYDRAD=$(M4B_HYDRAD) HYDRA_M4B_STUNTEST=$(M4B_STUNTEST) HYDRA_M4B_SOCKETTEST=$(M4B_SOCKETTEST) HYDRA_M5A_ICEHELPER=$(M5A_ICEHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5B_' -test.count=5 -test.timeout=20m

test-m5c1:
	go test -count=1 ./internal/iceconnect/...
	go test -count=1 ./test/integration -run '^TestM5C1_'

test-m5c1-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M5c1 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5c1 FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C1_' -test.count=1 -test.timeout=10m

test-m5c1-soak: test-m4b-build
	go test -race -count=100 ./internal/iceconnect -run '^TestDirectHostConnectivity/IPv4$$'
	go test -race -count=50 ./internal/iceconnect -run '^TestDirectHostConnectivity/IPv6$$'
	@command -v sudo >/dev/null || { echo "M5c1 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5c1 soak FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C1_DirectSocketOwnership/(ipv4|ipv6)$$' -test.count=10 -test.timeout=20m

test-m5c2:
	go test -count=1 ./internal/iceconnect/...
	go test -count=1 ./internal/iceadapter/...
	$(MAKE) test-m5c2-netns

test-m5c2-netns: test-m4b-build
	@command -v sudo >/dev/null || { echo "M5c2 FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5c2 FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) \
		HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C2_' -test.count=1 -test.timeout=10m

test-m5c2-soak: test-m4b-build
	go test -race -count=25 ./internal/iceconnect/... -run '^TestCandidateSnapshotSupportsServerAndPeerReflexiveDiagnostics$$'
	@command -v sudo >/dev/null || { echo "M5c2 soak FAIL: sudo is unavailable" >&2; exit 1; }
	@sudo -n true || { echo "M5c2 soak FAIL: an active sudo credential/CAP_NET_ADMIN is required" >&2; exit 1; }
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C2_ControlledNATConnectivity/A-nat-eim-apdf-to-public$$' -test.count=5 -test.timeout=10m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C2_ControlledNATConnectivity/B-public-to-nat-eim-apdf$$' -test.count=5 -test.timeout=10m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C2_ControlledNATConnectivity/C-nat-eim-apdf-to-nat-eim-apdf$$' -test.count=10 -test.timeout=15m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C2_ControlledNATConnectivity/D-nat-eim-eif-to-nat-apdm-apdf-prflx$$' -test.count=10 -test.timeout=15m
	sudo -n env HYDRA_M4B_PRIVILEGED=1 HYDRA_M4B_RFC5780=$(M4B_RFC5780) HYDRA_M5B_SIGNALHELPER=$(M5B_SIGNALHELPER) \
		$(M4B_RUNNER) -test.v -test.run '^TestM5C2_ControlledNATConnectivity/E-nat-apdm-apdf-to-nat-apdm-apdf$$' -test.count=5 -test.timeout=15m

clean-m4b:
	./scripts/netns/cleanup_m4b.sh

demo: build
	./scripts/demo_m0_m2.sh

clean:
	go clean -testcache
	rm -f irviod bin/irviod
