#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

set -euo pipefail

demo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
demo_tmp=$(mktemp -d /tmp/hydra-m0-m2-demo.XXXXXX)
b_pid=""
c_pid=""

cleanup() {
  set +e
  if [[ -n "$b_pid" ]]; then
    kill "$b_pid" 2>/dev/null
    wait "$b_pid" 2>/dev/null
  fi
  if [[ -n "$c_pid" ]]; then
    kill "$c_pid" 2>/dev/null
    wait "$c_pid" 2>/dev/null
  fi
  rm -rf -- "$demo_tmp"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

wait_for_info() {
  local file=$1
  local pid=$2
  local log=$3
  for _ in $(seq 1 100); do
    if python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["peer_id"] and d["listen_addrs"]' "$file" 2>/dev/null; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf 'hydrad exited before node info was ready:\n' >&2
      sed -n '1,200p' "$log" >&2
      return 1
    fi
    sleep 0.1
  done
  printf 'timed out waiting for node info\n' >&2
  return 1
}

bootstrap_from_info() {
  python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
addresses = d["listen_addrs"]
loopback = [a for a in addresses if a.startswith("/ip4/127.0.0.1/")]
ipv4 = [a for a in addresses if a.startswith("/ip4/")]
print((loopback or ipv4)[0])
' "$1"
}

mkdir -p "$demo_tmp/a" "$demo_tmp/b" "$demo_tmp/c"

"$demo_root/bin/hydrad" \
  --data-dir "$demo_tmp/b" \
  --listen-port 0 \
  --print-node-info \
  >"$demo_tmp/b-info.json" 2>"$demo_tmp/b.log" &
b_pid=$!
wait_for_info "$demo_tmp/b-info.json" "$b_pid" "$demo_tmp/b.log"
b_bootstrap=$(bootstrap_from_info "$demo_tmp/b-info.json")

"$demo_root/bin/hydrad" \
  --data-dir "$demo_tmp/c" \
  --listen-port 0 \
  --bootstrap "$b_bootstrap" \
  --exit \
  --country DE \
  --print-node-info \
  >"$demo_tmp/c-info.json" 2>"$demo_tmp/c.log" &
c_pid=$!
wait_for_info "$demo_tmp/c-info.json" "$c_pid" "$demo_tmp/c.log"
c_peer=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["peer_id"])' "$demo_tmp/c-info.json")

"$demo_root/bin/hydrad" \
  --data-dir "$demo_tmp/a" \
  --listen-port 0 \
  --bootstrap "$b_bootstrap" \
  --find-exits DE \
  --find-timeout 20s \
  --find-once \
  >"$demo_tmp/a-result.json" 2>"$demo_tmp/a.log"

python3 -c '
import json, sqlite3, sys
result_path, database_path, expected_peer = sys.argv[1:]
result = json.load(open(result_path))
assert expected_peer in result["providers"], (result, expected_peer)
with sqlite3.connect(database_path) as db:
    row = db.execute("SELECT node_id, country, capabilities FROM peers WHERE node_id = ?", (expected_peer,)).fetchone()
assert row is not None and row[1] == "DE" and row[2] & 2, row
print(json.dumps({"discovery": result, "sqlite_peer": {"node_id": row[0], "country": row[1], "capabilities": row[2]}}, indent=2))
' "$demo_tmp/a-result.json" "$demo_tmp/a/hydra.db" "$c_peer"

kill "$b_pid"
wait "$b_pid" 2>/dev/null || true
b_pid=""
printf 'M0-M2 demo passed: A discovered and persisted C through bootstrap B.\n'
