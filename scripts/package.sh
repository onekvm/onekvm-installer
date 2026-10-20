#!/bin/sh
set -eu

version=${1:?version is required}
destination="dist/v${version}"
mkdir -p "$destination"
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

for architecture in riscv64 arm64 amd64; do
    name="onekvm-installer-${version}-linux-${architecture}"
    mkdir -p "$staging/$name"
    install -m0755 "dist/onekvm-installer-linux-${architecture}" "$staging/$name/onekvm-installer"
    install -m0644 README.md LICENSE "$notices" "$staging/$name/"
    install -m0644 vendor/modules.txt "$staging/$name/DEPENDENCIES.txt"
    printf '%s\n' "$version" >"$staging/$name/VERSION"
    tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner \
        -C "$staging" -cf "$staging/$name.tar" "$name"
    gzip -n -c "$staging/$name.tar" >"$destination/$name.tar.gz"
done

install -m0755 onekvm.sh "$destination/onekvm.sh"
(cd "$destination" && sha256sum ./onekvm.sh ./*.tar.gz >SHA256SUMS)
printf 'Packages: %s\n' "$destination"
