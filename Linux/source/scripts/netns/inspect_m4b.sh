#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

set -euo pipefail

printf 'HYDRA M4b namespaces:\n'
ip netns list | awk '$1 ~ /^hm4b-/ {print}' | sort

printf '\nHYDRA M4b host veth candidates:\n'
ip -br link | awk '$1 ~ /^hy[a-z0-9][0-9a-f]{4}(@|$)/ {print}' | sort

printf '\nHYDRA M4b artifacts:\n'
find /tmp -maxdepth 1 -type d -name 'hydra-m4b-[0-9a-f]*' -print | sort
