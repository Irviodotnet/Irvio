#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

# Explicit allowlist packaging. Never tar the repository, profile or /var/lib.
set -Eeuo pipefail
REPO=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
if (($# < 2 || $# > 3)) || [[ ${3:-} != '' && ${3:-} != --private-preview ]]; then
    echo 'Usage: packaging/ubuntu/build.sh OUTPUT_DIRECTORY DBIP_COUNTRY_MMDB [--private-preview]' >&2
    exit 2
fi
OUT=$(realpath -m -- "$1")
MMDB=$(realpath -e -- "$2")
NAME=${IRVIO_PACKAGE_NAME:-irvio-1.1.0-alpha.20261005.4-ubuntu-amd64}
[[ $NAME =~ ^irvio-[a-zA-Z0-9._-]+$ ]] || { echo 'Invalid package name.' >&2; exit 2; }
if [[ ${3:-} == --private-preview ]]; then
    source_status=$(git -C "$REPO" status --porcelain --untracked-files=all) ||
        { echo 'Cannot verify private preview source state.' >&2; exit 1; }
    [[ -z $source_status ]] ||
        { echo 'Private preview requires a clean committed checkout, including untracked files.' >&2; exit 1; }
    NAME=irvio-private-preview-$(git -C "$REPO" rev-parse --short=12 HEAD)-ubuntu-amd64
fi
SRC=$REPO/packaging/ubuntu
mkdir -p "$OUT"
[[ ! -e $OUT/$NAME.tar.gz ]] || { echo 'Archive already exists; choose a fresh output directory.' >&2; exit 1; }
work=$(mktemp -d "${TMPDIR:-/tmp}/irvio-build.XXXXXX")
bundle=$work/$NAME
mkdir -p "$bundle/payload/bin" "$bundle/payload/ui" "$bundle/payload/geo" "$bundle/integration/systemd" "$bundle/licenses/go" "$bundle/licenses/flutter" "$bundle/scripts"
cd "$REPO"
go run ./packaging/ubuntu/verifygeo "$MMDB"
[[ $(sha256sum "$MMDB" | cut -d ' ' -f1) == d284ae2e7427fe33d83465e1506b2b21aae47eb8a9b099f8f4dac6a98c99f041 ]] ||
    { echo 'This dated package expects the verified September 2026 DB-IP Country Lite database.' >&2; exit 1; }
CGO_ENABLED=0 go build -trimpath -o "$bundle/payload/bin/irviod" ./cmd/irviod
CGO_ENABLED=0 go build -trimpath -o "$bundle/payload/bin/irvio" ./cmd/irvio
ui_source=$work/ui-source
mkdir -p "$ui_source"
# Never rebuild over a workstation UI that may currently be running.
rsync -a --exclude=build --exclude=.dart_tool --exclude=linux/flutter/ephemeral ui/ "$ui_source/"
(
    cd "$ui_source"
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy -u FTP_PROXY -u ftp_proxy \
        flutter pub get --offline
    env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u http_proxy -u https_proxy -u all_proxy -u FTP_PROXY -u ftp_proxy \
        flutter build linux --release --no-pub
)
cp -a "$ui_source/build/linux/x64/release/bundle/." "$bundle/payload/ui/"
install -m 0644 "$MMDB" "$bundle/payload/geo/country.mmdb"
install -m 0644 "$SRC/DB-IP-ATTRIBUTION.md" "$bundle/payload/geo/ATTRIBUTION.md"
install -m 0644 "$SRC/DB-IP-ATTRIBUTION.md" "$bundle/licenses/DB-IP-ATTRIBUTION.md"
install -m 0644 ui/assets/flags/LICENSE.txt "$bundle/licenses/FLAG-ICONS-LICENSE.txt"
install -m 0644 ui/assets/flags/SOURCE.txt "$bundle/licenses/FLAG-ICONS-SOURCE.txt"
install -m 0755 "$SRC/install.sh" "$SRC/uninstall.sh" "$bundle/"
install -m 0644 "$SRC/scripts/common.sh" "$bundle/scripts/"
install -m 0644 "$SRC/scripts/server.sh" "$SRC/scripts/server-health.py" "$bundle/scripts/"
install -m 0755 "$SRC/scripts/check-server.sh" "$bundle/scripts/"
install -m 0644 "$SRC/integration/systemd/irvio-server.service" "$bundle/integration/systemd/"
sed "s/irvio-1.1.0-alpha.20261005.4-ubuntu-amd64/$NAME/g" "$SRC/install.md" > "$bundle/install.md"
install -m 0755 "$SRC/integration/irvio" "$bundle/integration/"
install -m 0644 "$SRC/integration/irvio.desktop" "$SRC/integration/INSTALL_MARKER" "$bundle/integration/"
for unit in irviod.service irvio.socket irvio-boot-recover.service irvio-persistent.service; do
    sed -e 's@/usr/local/bin/irviod@/opt/irvio/bin/irviod@g' -e 's/HYDRA/IRVIO/g' \
        "$REPO/packaging/systemd/$unit" > "$bundle/integration/systemd/$unit"
done

# Preserve notices for linked Go modules and Dart/Flutter/native plugins.
go list -deps -f '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}' ./cmd/irviod ./cmd/irvio | sort -u > "$work/go-modules.txt"
cut -d '|' -f 1,2 "$work/go-modules.txt" > "$bundle/licenses/GO-MODULES.txt"
while IFS='|' read -r module version directory; do
    [[ -n $directory && $module != github.com/Irviodotnet/Irvio ]] || continue
    dest=$bundle/licenses/go/$module@$version
    for notice in "$directory"/LICENSE* "$directory"/COPYING* "$directory"/NOTICE*; do
        [[ -f $notice ]] || continue
        mkdir -p "$dest"
        install -m 0644 "$notice" "$dest/"
    done
    case $module in
        github.com/hashicorp/golang-lru|github.com/libp2p/go-yamux/v5)
            # Corresponding, unmodified upstream MPL-covered module sources.
            mkdir -p "$dest/source"
            rsync -am --exclude='*_test.go' --exclude='.*' --include='*/' \
                --include='*.go' --include='go.mod' --include='go.sum' --exclude='*' \
                "$directory/" "$dest/source/" ;;
    esac
done < "$work/go-modules.txt"
jq -r '.packages[] | [.name, .rootUri] | @tsv' "$ui_source/.dart_tool/package_config.json" > "$work/dart-packages.txt"
while IFS=$'\t' read -r package uri; do
    [[ $uri == file://* ]] || continue
    directory=${uri#file://}
    for notice in "$directory"/LICENSE* "$directory"/COPYING* "$directory"/NOTICE*; do
        [[ -f $notice ]] || continue
        mkdir -p "$bundle/licenses/flutter/$package"
        install -m 0644 "$notice" "$bundle/licenses/flutter/$package/"
    done
    if [[ $package == flutter ]]; then
        install -m 0644 "$directory/../../LICENSE" "$bundle/licenses/flutter/FLUTTER-SDK-LICENSE"
        install -m 0644 "$directory/../../bin/cache/artifacts/engine/linux-x64-release/LICENSE.flutter_gtk.md" "$bundle/licenses/flutter/"
    fi
done < "$work/dart-packages.txt"
install -m 0644 "$bundle/payload/ui/data/flutter_assets/NOTICES.Z" "$bundle/licenses/flutter/NOTICES.Z"
install -m 0644 "$REPO/LICENSE" "$bundle/licenses/AGPL-3.0-only.txt"
go version -m "$bundle/payload/bin/irviod" > "$bundle/GO_BUILD_INFO.txt"
source_head=unversioned-snapshot
source_branch=unversioned-snapshot
source_state='source snapshot; not a tagged release'
diff_sha256=unavailable
if [[ $(git rev-parse --show-toplevel 2>/dev/null || true) == "$REPO" ]]; then
    source_head=$(git rev-parse HEAD)
    source_branch=$(git branch --show-current)
    diff_sha256=$(git diff --binary HEAD | sha256sum | cut -d ' ' -f1)
    source_state='working-tree snapshot; not a tagged release'
fi
jq -n \
    --arg package "$NAME" --arg head "$source_head" --arg branch "$source_branch" \
    --arg built_at "$(date -u +%FT%TZ)" --arg go "$(go version)" --arg flutter "$(flutter --version | sed -n '1p')" \
    --arg diff_sha256 "$diff_sha256" \
    --arg source_state "$source_state" \
    --arg irviod_sha256 "$(sha256sum "$bundle/payload/bin/irviod" | cut -d ' ' -f1)" \
    --arg console_sha256 "$(sha256sum "$bundle/payload/bin/irvio" | cut -d ' ' -f1)" \
    --arg ui_library_sha256 "$(sha256sum "$bundle/payload/ui/lib/libapp.so" | cut -d ' ' -f1)" \
    --arg geoip_sha256 "$(sha256sum "$bundle/payload/geo/country.mmdb" | cut -d ' ' -f1)" \
    '{package:$package, target:"Desktop and server: Ubuntu 24.04/26.04 amd64", source_head:$head, source_branch:$branch,
      source_state:$source_state,
      tracked_diff_sha256:$diff_sha256, built_at_utc:$built_at, go:$go, flutter:$flutter,
      daemon_build:"CGO_ENABLED=0 go build -trimpath ./cmd/irviod", ui_build:"flutter build linux --release --no-pub",
      irviod_sha256:$irviod_sha256, console_sha256:$console_sha256, ui_library_sha256:$ui_library_sha256, geoip_sha256:$geoip_sha256,
      geoip_source:"https://download.db-ip.com/free/dbip-country-lite-2026-09.mmdb.gz"}' > "$bundle/BUILD_INFO.json"
chmod -R go-w "$bundle"
(
    cd "$bundle"
    find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > "$work/package-checksums"
    mv "$work/package-checksums" SHA256SUMS
    chmod 0644 SHA256SUMS
    sha256sum --check --strict --quiet SHA256SUMS
)
tar --sort=name --owner=0 --group=0 --numeric-owner -C "$work" -czf "$OUT/$NAME.tar.gz" "$NAME"
(cd "$OUT" && sha256sum "$NAME.tar.gz" > "$NAME.tar.gz.sha256")
install -m 0644 "$bundle/install.md" "$OUT/install.md"
install -m 0644 "$bundle/BUILD_INFO.json" "$OUT/BUILD_INFO.json"
printf 'Archive: %s\nStaging (retained for validation): %s\n' "$OUT/$NAME.tar.gz" "$bundle"
