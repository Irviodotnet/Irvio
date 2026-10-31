# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    systemd iproute2 nftables procps shellcheck desktop-file-utils file binutils \
    libgtk-3-0t64 libayatana-appindicator3-1 libstdc++6 libgl1 libegl1 \
    libnotify-bin ca-certificates
RUN apt-get update && apt-get install -y --no-install-recommends xvfb xauth libgles2
