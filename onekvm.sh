#!/bin/sh
# Bootstrap only: device detection and installation belong to the Go installer.
# Keep the entry point last so a piped script is read before opening the TUI.
set -eu

fail() {
    printf 'OneKVM: %s\n' "$*" >&2
    exit 1
}

download() {
    curl --fail --location --show-error --progress-bar --retry 2 \
        --connect-timeout 15 --max-time 300 --output "$2" "$1"
}

cleanup() {
    if [ -n "$workdir" ]; then
        rm -rf "$workdir"
    fi
}

main() {
    for dependency in uname mktemp awk sha256sum chmod rm; do
        command -v "$dependency" >/dev/null 2>&1 || fail "Required command not found: $dependency"
    done

    system=$(uname -s)
    [ "$system" = Linux ] || fail "Unsupported system: $system (Linux is required)"
    machine=$(uname -m)
    case "$machine" in
        riscv64) architecture=riscv64 ;;
        aarch64|arm64) architecture=arm64 ;;
        *) fail "Unsupported architecture: $machine (supported: riscv64, arm64)" ;;
    esac

    command -v curl >/dev/null 2>&1 || fail 'Install curl, then try again.'

    repository=${ONEKVM_INSTALLER_REPOSITORY:-onekvm/onekvm-installer}
    release_url=${ONEKVM_INSTALLER_RELEASE_URL:-https://github.com/$repository/releases/latest/download}
    release_url=${release_url%/}
    case "$release_url" in
        https://*|http://*) ;;
        *) fail 'ONEKVM_INSTALLER_RELEASE_URL must be an HTTP(S) URL.' ;;
    esac

    umask 077
    workdir=''
    trap cleanup 0
    trap 'exit 130' INT
    trap 'exit 143' TERM
    trap 'exit 129' HUP
    workdir=$(mktemp -d "${TMPDIR:-/tmp}/onekvm-installer.XXXXXX") || fail 'Cannot create a temporary directory.'

    printf 'OneKVM Installer · Linux %s\n' "$architecture" >&2
    download "$release_url/SHA256SUMS" "$workdir/SHA256SUMS" || fail 'Cannot download the release checksums.'

    # Resolve the version from the manifest, without a GitHub API request or jq.
    # Accept one matching asset only; never use paths supplied by the manifest.
    awk -v arch="$architecture" '
        NF == 2 && length($1) == 64 && $1 !~ /[^0-9a-fA-F]/ {
            name = $2
            sub(/^\*/, "", name)
            sub(/^\.\//, "", name)
            if (name ~ ("^onekvm-installer-[0-9][0-9A-Za-z.+_-]*-linux-" arch "$")) {
                count++
                selected = tolower($1) " " name
            }
        }
        END {
            if (count != 1) exit 1
            print selected
        }
    ' "$workdir/SHA256SUMS" >"$workdir/selected" || fail "The release must contain exactly one Linux $architecture package with a SHA-256 checksum."
    IFS=' ' read -r expected_hash binary_name <"$workdir/selected"

    printf 'Downloading %s\n' "$binary_name" >&2
    download "$release_url/$binary_name" "$workdir/$binary_name" || fail 'Cannot download the installer package.'
    printf '%s  %s\n' "$expected_hash" "$binary_name" >"$workdir/selected.sha256"
    (cd "$workdir" && sha256sum -c selected.sha256) >&2 || fail 'SHA-256 verification failed. The installer was not started.'

    binary="$workdir/$binary_name"
    [ -f "$binary" ] && [ ! -L "$binary" ] || fail 'The package does not contain an installer executable.'
    chmod 700 "$binary"

    # stdin is the script pipe with `curl ... | sh`. Bubble Tea needs the
    # controlling terminal instead. Read-only commands also work without a TTY.
    result=0
    if [ -t 0 ]; then
        "$binary" "$@" || result=$?
    elif (: </dev/tty) 2>/dev/null; then
        "$binary" "$@" </dev/tty || result=$?
    else
        "$binary" "$@" </dev/null || result=$?
    fi
    return "$result"
}

main "$@"
