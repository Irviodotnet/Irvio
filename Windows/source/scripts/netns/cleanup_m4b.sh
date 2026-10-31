#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

set -euo pipefail

namespace_pattern='^hm4b-[0-9a-f]{8}-[0-9a-f]{3}-(a|b|c|client|client[1-3]|client-b|client-b2|exit|exit-[a-d]|internet|internet-[a-d]|server|switch|router|router1|router2|probe|nat|subject|subject6|verifier|verifier6|wan1|wan2)$'
process_pattern='^/tmp/(hydra-m4b-(natprobe|rfc5780|socketmux|hydrad|stunprobe\.test|socketmux\.test|privileged\.test)|hydra-m5a-iceadapter|hydra-m5b-icesignal|hydra-m5c3-networkstate|hydra-m7-internet|hydra-m8-killswitch)( |$)'

mapfile -t owned_pids < <(pgrep -f "$process_pattern" | sort -n || true)
if ((${#owned_pids[@]} > 0)) && ((EUID != 0)); then
  exec sudo -n -- "$0" "$@"
fi

for pid in "${owned_pids[@]}"; do
  kill -TERM "$pid" 2>/dev/null || true
done
for _ in {1..20}; do
  remaining=0
  for pid in "${owned_pids[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      remaining=1
      break
    fi
  done
  ((remaining == 0)) && break
  sleep 0.05
done
for pid in "${owned_pids[@]}"; do
  kill -KILL "$pid" 2>/dev/null || true
done

mapfile -t namespaces < <(ip netns list | awk '{print $1}' | sort)
owned=()
for namespace in "${namespaces[@]}"; do
  if [[ "$namespace" =~ $namespace_pattern ]]; then
    owned+=("$namespace")
  elif [[ "$namespace" == hm4b-* ]]; then
    printf 'Refusing unexpected HYDRA namespace name: %s\n' "$namespace" >&2
    exit 1
  fi
done

if ((${#owned[@]} > 0)) && ((EUID != 0)); then
  exec sudo -n -- "$0" "$@"
fi

for namespace in "${owned[@]}"; do
  ip netns delete "$namespace"
done

mapfile -t links < <(ip -o link show | awk -F': ' '{print $2}' | cut -d@ -f1 | sort)
for link in "${links[@]}"; do
  if [[ "$link" =~ ^hy[a-z0-9][0-9a-f]{4}$ ]]; then
    if ((EUID != 0)); then
      exec sudo -n -- "$0" "$@"
    fi
    ip link delete "$link"
  elif [[ "$link" == hy* ]]; then
    printf 'Refusing unexpected HYDRA veth name: %s\n' "$link" >&2
    exit 1
  fi
done

shopt -s nullglob
for path in /tmp/hydra-m4b-[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]; do
	[[ -d "$path" && "$path" != /tmp && "$path" != / ]] || {
		printf 'Refusing unsafe artifact path: %s\n' "$path" >&2
		exit 1
	}
	if ((EUID != 0)) && [[ ! -w "$path" ]]; then
		exec sudo -n -- "$0" "$@"
	fi
	rm -rf -- "$path"
done

for path in /tmp/hydra-m4b-runtime-[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]-[0-9a-f][0-9a-f][0-9a-f]-*; do
	[[ -d "$path" && "$path" != /tmp && "$path" != / ]] || {
		printf 'Refusing unsafe runtime path: %s\n' "$path" >&2
		exit 1
	}
	basename=${path##*/}
	[[ "$basename" =~ ^hydra-m4b-runtime-[0-9a-f]{8}-[0-9a-f]{3}-[a-z0-9-]+$ ]] || {
		printf 'Refusing unexpected runtime path: %s\n' "$path" >&2
		exit 1
	}
	if ((EUID != 0)) && [[ ! -w "$path" ]]; then
		exec sudo -n -- "$0" "$@"
	fi
	rm -rf -- "$path"
done

for file in \
  /tmp/hydra-m4b-natprobe \
  /tmp/hydra-m4b-rfc5780 \
  /tmp/hydra-m4b-socketmux \
  /tmp/hydra-m4b-hydrad \
  /tmp/hydra-m4b-stunprobe.test \
  /tmp/hydra-m4b-socketmux.test \
  /tmp/hydra-m4b-privileged.test \
  /tmp/hydra-m5a-iceadapter \
  /tmp/hydra-m5b-icesignal \
  /tmp/hydra-m5c3-networkstate \
  /tmp/hydra-m7-internet \
  /tmp/hydra-m8-killswitch; do
  [[ "$file" == /tmp/hydra-m4b-* || "$file" == /tmp/hydra-m5a-iceadapter || "$file" == /tmp/hydra-m5b-icesignal || "$file" == /tmp/hydra-m5c3-networkstate || "$file" == /tmp/hydra-m7-internet || "$file" == /tmp/hydra-m8-killswitch ]] || exit 1
  rm -f -- "$file"
done

printf 'HYDRA M4b cleanup complete; removed %d process(es) and %d namespace(s).\n' "${#owned_pids[@]}" "${#owned[@]}"
