#!/bin/sh
set -eu

version=${1:?version is required}
destination="dist/v${version}"
mkdir -p "$destination"
rm -f "$destination"/onekvm-installer-*-linux-* \
    "$destination"/onekvm.sh "$destination"/THIRD_PARTY_NOTICES.txt \
    "$destination"/SHA256SUMS
staging=$(mktemp -d "$destination/.package.XXXXXX")
trap 'rm -rf "$staging"' EXIT HUP INT TERM

notices="$staging/THIRD_PARTY_NOTICES.txt"
printf 'OneKVM Installer %s — third-party licenses\n' "$version" >"$notices"
find vendor -type f \( -name 'LICENSE*' -o -name 'COPYING*' \) | LC_ALL=C sort >"$staging/licenses.list"
# This module declares its MIT license in README rather than a LICENSE file.
printf '%s\n' vendor/github.com/mattn/go-localereader/README.md >>"$staging/licenses.list"
while IFS= read -r license; do
    printf '\n===== %s =====\n\n' "$license" >>"$notices"
    cat "$license" >>"$notices"
done <"$staging/licenses.list"

for architecture in riscv64 arm64; do
    name="onekvm-installer-${version}-linux-${architecture}"
    install -m0755 "dist/onekvm-installer-linux-${architecture}" "$destination/$name"
done

install -m0755 onekvm.sh "$destination/onekvm.sh"
install -m0644 "$notices" "$destination/THIRD_PARTY_NOTICES.txt"
(cd "$destination" && sha256sum ./onekvm.sh ./onekvm-installer-*-linux-* ./THIRD_PARTY_NOTICES.txt >SHA256SUMS)
printf 'Packages: %s\n' "$destination"
