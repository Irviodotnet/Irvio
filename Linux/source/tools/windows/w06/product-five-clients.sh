#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

set -euo pipefail
umask 077
api=${IRVIO_W5_API:?explicit Local API helper required}
daemon=${IRVIO_W5_DAEMON:?explicit frozen Linux daemon required}
launcher=${IRVIO_W5_DNS_LAUNCHER:?explicit isolated DNS launcher required}
wan=${IRVIO_W5_WAN:?explicit physical interface required}
country=${IRVIO_W5_COUNTRY:?explicit Windows Exit country required}
dhcp_hook=${IRVIO_W5_DHCP_HOOK:?explicit DHCP hook required}
resolv_conf=${IRVIO_W5_RESOLV_CONF:?explicit namespace resolver file required}
bootstrap_store=${IRVIO_W5_BOOTSTRAP_STORE:?explicit stored public bootstrap required}
test "$(id -u)" = 0
test "$(readlink /proc/self/ns/net)" = "$(readlink /proc/1/ns/net)"
test "$(readlink /proc/self/ns/mnt)" = "$(readlink /proc/1/ns/mnt)"
ip link show dev "$wan" >/dev/null
lab_dir=$(mktemp -d /tmp/irvio-five-product-XXXXXX)
printf 'LAB_DIR=%s\n' "$lab_dir"
suffix=${lab_dir##*-}
names=(); pids=(); mounts=(); dhcp_pids=(); inner_capture_pids=()
capture_pid=''
timing_format='{"bytes":%{size_download},"seconds":%{time_total},"bytes_per_second":%{speed_download},"dns_seconds":%{time_namelookup},"tcp_seconds":%{time_connect},"tls_seconds":%{time_appconnect},"ttfb_seconds":%{time_starttransfer},"remote_ip":"%{remote_ip}","remote_port":%{remote_port},"local_port":%{local_port}}\n'
ip -j route show table all > "$lab_dir/routes-before.json"
resolvectl status > "$lab_dir/dns-before.txt"
nft list ruleset > "$lab_dir/nft-before.txt"
test -x "$launcher"
"$api" private-summary /run/irvio/control.sock > "$lab_dir/installed-before.json"
grep -q '"state":"CONNECTION_STATE_DISCONNECTED"' "$lab_dir/installed-before.json"
"$api" bundle-from-store "$bootstrap_store" > "$lab_dir/bootstrap.txt"
mapfile -t peers < <("$api" bundle-addresses "$lab_dir/bootstrap.txt")
test "${#peers[@]}" -ge 2
bundle=$(<"$lab_dir/bootstrap.txt")
stun=''
underlay=()
for peer in "${peers[@]}"; do
 underlay+=(--underlay-target "$peer")
 if [[ -z "$stun" && "$peer" =~ ^/ip4/([0-9.]+)/tcp/ ]]; then stun=${BASH_REMATCH[1]}:3478; fi
done
test -n "$stun"
cleanup() {
 result=$?
 trap - EXIT
 set +e
 for i in "${!pids[@]}"; do
  "$api" disconnect "$lab_dir/client-$i/control.sock" > "$lab_dir/client-$i/disconnect.json" 2>&1
 done
 for pid in "${pids[@]}"; do kill -TERM "$pid" 2>/dev/null; done
 for pid in "${pids[@]}"; do
  for retry in $(seq 1 100); do kill -0 "$pid" 2>/dev/null || break; sleep .1; done
  if kill -0 "$pid" 2>/dev/null; then
   if tr '\0' ' ' < "/proc/$pid/cmdline" | grep -Fq "$lab_dir/client-"; then kill -KILL "$pid"; result=1; fi
  fi
  wait "$pid" 2>/dev/null
 done
 if test -n "$capture_pid"; then kill -INT "$capture_pid" 2>/dev/null; wait "$capture_pid"; fi
 for pid in "${inner_capture_pids[@]}"; do kill -INT "$pid" 2>/dev/null; wait "$pid"; done
 for pid in "${dhcp_pids[@]}"; do kill -USR2 "$pid" 2>/dev/null; done
 sleep 1
 for pid in "${dhcp_pids[@]}"; do kill -TERM "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; done
 for name in "${names[@]}"; do
  if test -n "$(ip netns pids "$name")"; then echo "ORPHANS:$name"; result=1; fi
  ip netns delete "$name"
  if test -f "/etc/netns/$name/resolv.conf"; then unlink "/etc/netns/$name/resolv.conf"; fi
  rmdir "/etc/netns/$name" 2>/dev/null
 done
 ip -j route show table all > "$lab_dir/routes-after.json"
 resolvectl status > "$lab_dir/dns-after.txt"
 nft list ruleset > "$lab_dir/nft-after.txt"
 cmp "$lab_dir/routes-before.json" "$lab_dir/routes-after.json" || result=1
 cmp "$lab_dir/dns-before.txt" "$lab_dir/dns-after.txt" || result=1
 cmp "$lab_dir/nft-before.txt" "$lab_dir/nft-after.txt" || result=1
 printf 'FIVE_CLIENT_RESULT=%s\n' "$result"
 exit "$result"
}
trap cleanup EXIT
tcpdump -i "$wan" -nn -s 160 -U -w "$lab_dir/udp.pcap" 'udp and portrange 41000-41004' > "$lab_dir/capture.log" 2>&1 &
capture_pid=$!
for i in 0 1 2 3 4; do
 name=w5${suffix:0:4}$i
 ip netns add "$name"; names+=("$name")
 clientif=${name}c
 ip link add link "$wan" name "$clientif" type macvlan mode bridge
 ip link set "$clientif" netns "$name"
 ip -n "$name" link set "$clientif" up; ip -n "$name" link set lo up
 install -d -m 0755 "/etc/netns/$name"
 install -m 0644 "$resolv_conf" "/etc/netns/$name/resolv.conf"
 profile=$lab_dir/client-$i
 install -d -m 0700 "$profile" "$profile/data"
 ip netns exec "$name" env IRVIO_DHCP_STATE="$profile/dhcp-address.txt" busybox udhcpc -f -n -t 5 -T 3 -i "$clientif" -s "$dhcp_hook" > "$profile/dhcp.log" 2>&1 &
 dhcp_pids+=("$!")
 for retry in $(seq 1 100); do test -s "$profile/dhcp-address.txt" && break; sleep .2; done
 test -s "$profile/dhcp-address.txt"
done
baseline_jobs=()
for i in 0 1 2 3 4; do
 ip netns exec "${names[$i]}" curl --noproxy '*' --fail --silent --show-error --max-time 90 --output /dev/null --write-out "$timing_format" 'https://speed.cloudflare.com/__down?bytes=1048576' > "$lab_dir/client-$i/direct-download.json" 2> "$lab_dir/client-$i/direct-error.txt" &
 baseline_jobs+=("$!")
done
baseline_result=0
for job in "${baseline_jobs[@]}"; do wait "$job" || baseline_result=1; done
test "$baseline_result" = 0
echo FIVE_DIRECT_DOWNLOAD_BASELINE_PASS
for i in 0 1 2 3 4; do
 profile=$lab_dir/client-$i
 name=${names[$i]}
 bypass=$(python3 -c 'import ipaddress,sys; print(ipaddress.ip_interface(sys.argv[1]).network)' "$(<"$profile/dhcp-address.txt")")
 runtime=$(mktemp -d /tmp/hydra-m4b-runtime-w5-XXXXXX); mounts+=("$runtime")
 ip netns exec "$name" "$launcher" --runtime-root "$runtime" "$daemon" --runtime --connect "$country" --protected-mode --rendezvous-v1 --udp-port "$((41000+i))" --data-dir "$profile/data" --bootstrap "$bundle" --stun4 "$stun" --tunnel-dns 1.1.1.1 --bypass-prefix "$bypass" --local-api-socket "$profile/control.sock" --local-api-group root --print-node-info --print-runtime-events "${underlay[@]}" > "$profile/stdout.log" 2> "$profile/stderr.log" &
 pids+=("$!")
done
for i in 0 1 2 3 4; do
 connected=false
 for retry in $(seq 1 100); do
  kill -0 "${pids[$i]}" || { tail -8 "$lab_dir/client-$i/stderr.log";exit 1; }
  if test -S "$lab_dir/client-$i/control.sock"; then
   state=$("$api" status "$lab_dir/client-$i/control.sock")
   printf '%s\n' "$state" >> "$lab_dir/client-$i/status.ndjson"
   case "$state" in *'"state":"CONNECTION_STATE_CONNECTED"'*) connected=true;break;; esac
  fi
  sleep 1
 done
 test "$connected" = true
 echo "CLIENT_$i CONNECTED"
done
echo ALL_FIVE_CONNECTED
if test "${IRVIO_W5_CAPTURE_TCP:-0}" = 1; then
 for i in 0 1 2 3 4; do
  ip netns exec "${names[$i]}" tcpdump -i any -nn -s 160 -U -w "$lab_dir/client-$i/tcp-headers.pcap" 'tcp port 443' > "$lab_dir/client-$i/tcp-capture.log" 2>&1 &
  inner_capture_pids+=("$!")
 done
 sleep 1
 for pid in "${inner_capture_pids[@]}"; do kill -0 "$pid"; done
fi
jobs=()
for i in 0 1 2 3 4; do
 (
  trap - EXIT
  for round in 1 2; do
   ip netns exec "${names[$i]}" curl --noproxy '*' --fail --silent --show-error --max-time 15 https://api.ipify.org > "$lab_dir/client-$i/egress-$round.txt"
   ip netns exec "${names[$i]}" getent ahostsv4 example.com > "$lab_dir/client-$i/dns-$round.txt"
   ip netns exec "${names[$i]}" curl --noproxy '*' --fail --silent --show-error --max-time 120 --output /dev/null --write-out "$timing_format" 'https://speed.cloudflare.com/__down?bytes=1048576' > "$lab_dir/client-$i/download-$round.json"
   python3 -c 'import json,sys; assert json.load(open(sys.argv[1]))["bytes"] == 1048576' "$lab_dir/client-$i/download-$round.json"
   echo "CLIENT_$i ROUND_$round DNS_HTTPS_1MiB_PASS"
  done
  if test "${IRVIO_W5_UPLOAD:-0}" = 1; then
   ip netns exec "${names[$i]}" head -c 1048576 /dev/zero | ip netns exec "${names[$i]}" curl --noproxy '*' --fail --silent --show-error --max-time 120 --output /dev/null --write-out '{"bytes":%{size_upload},"seconds":%{time_total}}\n' --data-binary @- 'https://speed.cloudflare.com/__up' > "$lab_dir/client-$i/upload.json"
   python3 -c 'import json,sys; assert json.load(open(sys.argv[1]))["bytes"] == 1048576' "$lab_dir/client-$i/upload.json"
   echo "CLIENT_$i UPLOAD_1MiB_PASS"
  fi
 ) > "$lab_dir/client-$i/probes.log" 2>&1 &
 jobs+=("$!")
done
probe_result=0
for job in "${jobs[@]}"; do wait "$job" || probe_result=1; done
test "$probe_result" = 0
if test "${IRVIO_W5_HOST_DISCONNECT:-0}" = 1; then
 printf 'ready\n' > "$lab_dir/clients-probed"
 echo FIVE_CLIENTS_WAITING_FOR_HOST_DISCONNECT
 deadline=$((SECONDS+180))
 until test -f "$lab_dir/host-disconnected"; do
  test "$SECONDS" -lt "$deadline" || { echo HOST_DISCONNECT_DEADLINE; exit 1; }
  sleep 1
 done
 physical_jobs=()
 for i in 0 1 2 3 4; do
  (
   trap - EXIT
   "$api" status "$lab_dir/client-$i/control.sock" > "$lab_dir/client-$i/host-down-state.json"
   source_ip=$(<"$lab_dir/client-$i/dhcp-address.txt")
   source_ip=${source_ip%/*}
   code=0
   ip netns exec "${names[$i]}" curl --noproxy '*' --interface "$source_ip" --fail --silent --show-error --connect-timeout 5 --max-time 15 --output /dev/null --write-out '%{size_download}' 'https://speed.cloudflare.com/__down?bytes=65536' > "$lab_dir/client-$i/host-down-physical-bytes.txt" 2> "$lab_dir/client-$i/host-down-physical-error.txt" || code=$?
   printf '%s\n' "$code" > "$lab_dir/client-$i/host-down-physical-code.txt"
   test "$code" -ne 0
   test "$(<"$lab_dir/client-$i/host-down-physical-bytes.txt")" = 0
  ) &
  physical_jobs+=("$!")
 done
 physical_result=0
 for job in "${physical_jobs[@]}"; do wait "$job" || physical_result=1; done
 test "$physical_result" = 0
 echo FIVE_HOST_DOWN_PHYSICAL_PROBES_BLOCKED
fi
sleep 12
for i in 0 1 2 3 4; do
 "$api" disconnect "$lab_dir/client-$i/control.sock" > "$lab_dir/client-$i/explicit-disconnect.json"
 echo "CLIENT_$i RELEASED"
 sleep 4
done
echo FIVE_PRODUCT_DOWNSTREAM_TRAFFIC_PASS
