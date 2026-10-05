#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

server_failure() {
    echo 'IRVIO server installation/check failed; an existing profile was not removed.' >&2
    echo 'Check the error above. Service: sudo systemctl status irvio-server.service --no-pager' >&2
    echo 'Logs: sudo journalctl -u irvio-server.service -n 100 --no-pager' >&2
    echo 'If installed, correct the cause, run sudo systemctl restart irvio-server.service, then sudo /opt/irvio/scripts/check-server.sh' >&2
}

server_firewall_mode() {
    if systemctl is-active --quiet firewalld.service; then
        die 'Active firewalld is not supported by this Ubuntu installer; have the administrator permit TCP/4000, UDP/3478,51820 and Exit forwarding before deployment.'
    fi
    firewall_mode=none
    if command -v ufw >/dev/null && LC_ALL=C ufw status | grep -qx 'Status: active'; then
        firewall_mode=ufw
    fi
    # Refuse custom nftables rather than assume an accept rule overrides another base chain.
    local tables rules
    tables=$(nft list tables)
    if [[ $firewall_mode == none && -n $tables ]]; then
        die 'Custom nftables configuration detected. Automatic firewall changes refused; review TCP/4000, UDP/3478,51820 and Exit forwarding with the administrator.'
    fi
    if [[ $firewall_mode == ufw ]] && grep -Ev '^table ip6? (filter|nat|mangle|raw|security)$' <<< "$tables" | grep -q '[^[:space:]]'; then
        die 'UFW coexists with custom nftables tables; automatic changes refused to protect existing host policy.'
    fi
    if [[ $firewall_mode == none ]] && command -v iptables-save >/dev/null; then
        rules=$(iptables-save)
        if grep -Eq '^(-A |:[^ ]+ (DROP|REJECT))' <<< "$rules"; then
            die 'Custom iptables configuration detected; automatic firewall changes refused.'
        fi
        if command -v ip6tables-save >/dev/null; then
            rules=$(ip6tables-save)
            if grep -Eq '^(-A |:[^ ]+ (DROP|REJECT))' <<< "$rules"; then
                die 'Custom IPv6 iptables configuration detected; automatic firewall changes refused.'
            fi
        fi
    fi
}

install_server() {
    export LC_ALL=C
    trap 'rc=$?; if ((rc != 0)); then server_failure; fi' EXIT
    require_root
    [[ $bootstrap =~ ^irvio-bootstrap:v1:[A-Za-z0-9_-]+={0,2}$ && ${#bootstrap} -le 16384 ]] ||
        die 'Supply one complete irvio-bootstrap:v1: bundle with --bootstrap (not a URL, placeholder or multiaddr).'
    check_bundle "$BUNDLE"
    [[ -r /etc/os-release ]] || die 'Cannot identify operating system.'
    . /etc/os-release
    [[ ${ID:-} == ubuntu && ( ${VERSION_ID:-} == 24.04 || ${VERSION_ID:-} == 26.04 ) ]] || die 'Server installer supports Ubuntu 24.04/26.04 only.'
    [[ $(dpkg --print-architecture) == amd64 ]] || die 'This archive requires amd64.'
    [[ -d /run/systemd/system && -c /dev/net/tun ]] || die 'A systemd host with /dev/net/tun is required.'
    [[ -s $BUNDLE/payload/geo/country.mmdb && -f $BUNDLE/payload/geo/ATTRIBUTION.md ]] || die 'GeoIP MMDB or attribution missing from archive.'
    if $check_only; then
        note 'Server bundle/host check OK; no service, firewall or dependencies changed.'
        return
    fi
    check_fresh_targets
    [[ $(systemctl show irvio-server.service -p LoadState --value) == not-found ]] || die 'Existing irvio-server.service: use a reviewed upgrade; identity and manual deployment are preserved.'
    [[ ! -e /etc/irvio/server.env && ! -L /etc/irvio/server.env ]] || die 'Existing /etc/irvio/server.env requires review.'
    [[ ! -L /etc/irvio ]] || die 'Refusing symlink /etc/irvio.'
    if $install_deps; then
        apt-get update
        apt-get install -y --no-install-recommends iproute2 nftables procps ca-certificates python3
    fi
    for tool in ip nft pgrep python3 ss journalctl; do command -v "$tool" >/dev/null || die "Missing $tool; install: sudo apt install iproute2 nftables procps ca-certificates python3"; done
    check_quiescent
    server_firewall_mode
    [[ -z $(ss -H -ltn 'sport = :4000') ]] || die 'TCP/4000 is occupied; stop or reconfigure the conflicting service.'
    [[ -z $(ss -H -lun 'sport = :3478 or sport = :51820') ]] || die 'UDP/3478 or UDP/51820 is occupied; no random-port fallback is permitted.'
    local stage
    stage=$(mktemp -d /opt/.irvio-server-install.XXXXXX)
    install -d -m 0755 "$stage/bin" "$stage/scripts" "$stage/integration/systemd"
    install -m 0755 "$BUNDLE/payload/bin/irviod" "$stage/bin/irviod"
    install -m 0755 "$BUNDLE/scripts/check-server.sh" "$stage/scripts/check-server.sh"
    install -m 0644 "$BUNDLE/scripts/server-health.py" "$stage/scripts/server-health.py"
    install -m 0644 "$BUNDLE/scripts/common.sh" "$BUNDLE/scripts/server.sh" "$stage/scripts/"
    install -m 0755 "$BUNDLE/uninstall.sh" "$stage/uninstall.sh"
    install -m 0644 "$BUNDLE/integration/systemd/irvio-server.service" "$stage/integration/systemd/"
    cp -a --no-preserve=ownership "$BUNDLE/licenses" "$stage/"
    install -m 0644 "$BUNDLE/BUILD_INFO.json" "$BUNDLE/install.md" "$stage/"
    printf '%s\n' irvio-ubuntu-server-v1 > "$stage/INSTALL_MARKER"
    chmod -R go-w "$stage"
    chmod 0755 "$stage"
    chown -R root:root "$stage"
    install -d -m 0700 "$STATE"
    install -d -m 0755 "$STATE/geo" /etc/irvio "$UNIT_DIR"
    if [[ ! -e $STATE/geo/country.mmdb ]]; then
        install -m 0644 "$BUNDLE/payload/geo/country.mmdb" "$STATE/geo/country.mmdb"
        install -m 0644 "$BUNDLE/payload/geo/ATTRIBUTION.md" "$STATE/geo/ATTRIBUTION.md"
    else
        note 'Existing GeoIP database preserved.'
    fi
    (umask 077; printf 'IRVIO_BOOTSTRAP=%s\n' "$bootstrap" > /etc/irvio/server.env)
    mv -T "$stage" "$PREFIX"
    if [[ $firewall_mode == ufw ]]; then
        ufw allow 4000/tcp comment 'IRVIO server'
        ufw allow 3478/udp comment 'IRVIO server'
        ufw allow 51820/udp comment 'IRVIO server'
        ufw allow in on hydra0 proto tcp to any port 51822 comment 'IRVIO inner lease control'
        ufw route allow in on hydra0 comment 'IRVIO Exit forwarding'
        note 'UFW persistent IRVIO permissions installed; unrelated rules and SSH unchanged.'
    else
        note 'No active host filtering detected; no firewall enabled or reset.'
    fi
    install -m 0644 "$PREFIX/integration/systemd/irvio-server.service" "$UNIT_DIR/irvio-server.service"
    systemctl daemon-reload
    systemctl enable --now irvio-server.service
    note 'Installed. Checking this service invocation (up to 5 minutes); this is not yet a readiness claim.'
    "$PREFIX/scripts/check-server.sh"
    trap - EXIT
}

uninstall_server() {
    [[ ! -L $PREFIX && $(stat -c %u "$PREFIX") == 0 && $(stat -c %u "$PREFIX/INSTALL_MARKER") == 0 ]] || die 'Unsafe server installation ownership.'
    cmp -s "$PREFIX/integration/systemd/irvio-server.service" "$UNIT_DIR/irvio-server.service" || die 'Modified server unit; manual review required.'
    [[ ! -d $UNIT_DIR/irvio-server.service.d ]] || die 'Server service overrides require manual review.'
    [[ -z $(findmnt -rn -o TARGET | awk '$0 == "/opt/irvio" || index($0, "/opt/irvio/") == 1') ]] || die 'Mounted filesystem inside installation; refusing removal.'
    systemctl disable --now irvio-server.service
    check_quiescent
    rm -f -- "$UNIT_DIR/irvio-server.service"
    systemctl daemon-reload
    rm -rf --one-file-system -- /opt/irvio
    note 'Server binaries/unit removed. Identity, GeoIP, bootstrap configuration and firewall permissions preserved for administrator review.'
}
