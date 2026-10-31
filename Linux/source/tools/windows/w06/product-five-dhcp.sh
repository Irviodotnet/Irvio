#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

set -euo pipefail
[[ "$interface" =~ ^w5[A-Za-z0-9]+c$ ]]
case "$1" in
 deconfig)
  ip -4 addr flush dev "$interface"
  ;;
 bound|renew)
  prefix=$(python3 -c 'import ipaddress,os; print(ipaddress.IPv4Network("0.0.0.0/"+os.environ["subnet"]).prefixlen)')
  ip -4 addr replace "$ip/$prefix" dev "$interface"
  gateway=${router%% *}
  ip route replace default via "$gateway" dev "$interface"
  printf '%s/%s\n' "$ip" "$prefix" > "$IRVIO_DHCP_STATE"
  ;;
esac
