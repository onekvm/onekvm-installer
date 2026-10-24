# OneKVM Installer

English | [简体中文](README.zh-CN.md)

OneKVM Installer is a standalone Go and Bubble Tea installer and recovery
utility. It can download or validate a OneKVM `.img`, collect first-boot
settings, write the image to the correct storage device, and verify the
result. The installer supports English, Simplified Chinese, and Traditional
Chinese and does not require Go or Python on the target device.

> **Build note:** This repository is an independent Go project. Release
> packages are built by GitHub Actions.

## Supported devices

| Device family | Architecture | Image profile | Installation target |
| --- | --- | --- | --- |
| NanoKVM SG200x (Cube, PCIe, Lite) | `riscv64` | `nanokvm` | SD card |
| NanoKVM Pro Desk | `arm64` | `pro-sd` | Direct SD-card write |
| NanoKVM Pro Desk | `arm64` | `pro-emmc` | Staged eMMC recovery install |

NanoKVM Pro PCIe is not currently available because its matching device-tree
and image are not included. The Pro SD path requires a device with the
matching SPL support; the installer checks this before writing.

## Install from a device shell

Run as root:

```sh
curl -fsSL https://onekvm.sh | sh
```

If the current shell is not root, run the shell on the right side of the pipe
with `sudo`:

```sh
curl -fsSL https://onekvm.sh | sudo sh
```

The bootstrap script detects `riscv64` or `arm64`, downloads the
matching package from the [latest GitHub Release](https://github.com/onekvm/onekvm-installer/releases/latest),
and verifies its SHA-256 checksum before starting the installer.

To install from an image URL, pass installer arguments after `sh -s --`:

```sh
curl -fsSL https://onekvm.sh \
  | sh -s -- install --url 'https://mirror.example/onekvm-nanokvm.img'
```

For a non-root shell, use `sudo sh -s -- install --url 'https://…'` on the
right side of the pipe. To mirror the installer package itself, set the
variable on the shell side of the pipe:

```sh
curl -fsSL https://onekvm.sh \
  | ONEKVM_INSTALLER_RELEASE_URL=https://mirror.example/installer sh
```

The device needs Linux, `curl`, and the common commands used by the
bootstrap script (`uname`, `mktemp`, `awk`, `sha256sum`, `tar`, `chmod`, and
`rm`). Installation itself must run as root.

## Installation flow

The wizard walks through these steps:

1. Choose the interface language.
2. Select a GitHub image, an online HTTP(S) image URL, or a local `.img` file.
3. Validate image metadata, profile, storage type, and SHA-256.
4. Create the first administrator and choose the time zone.
5. Copy or configure Ethernet and Wi-Fi settings independently.
6. Run the checks and solution hooks for the detected device and image.
7. Review the target disk and confirm the write.
8. Write and verify the image, then reboot immediately or later.

The target disk is shown before any destructive operation. Writing an image
erases the target medium. The installer refuses to install over an existing
OneKVM system and does not expose Wi-Fi passwords in the confirmation page,
JSON output, or final access summary.

NanoKVM uses its verified SD-card layout and recovery bootstrap. Pro Desk SD
is written directly without staging or entering recovery mode, while Pro Desk
eMMC stages the image and reboots into recovery. The Pro SD path preserves the
eMMC boot firmware and U-Boot environment. After installation, `/boot` is
created with a 128 MiB layout.

NanoKVM recovery uses the matching FIP, kernel and environment from the
verified OneKVM image. It replaces the previous boot files before rebooting
into recovery, then writes the complete image from RAM. The existing boot
partition is checked against the required file capacity, without a fixed size
list or assumptions about the previous system's boot configuration.

## Command-line inspection and diagnostics

Running the binary without a command opens the interactive wizard. The
following commands are read-only unless explicitly stated:

```sh
./onekvm-installer inspect
./onekvm-installer network --interface eth0
./onekvm-installer releases
./onekvm-installer releases --profile nanokvm --tag v0.1.0
./onekvm-installer verify --image /data/OneKVM.img --profile nanokvm
./onekvm-installer solution --image /data/OneKVM.img
./onekvm-installer solution --image /data/OneKVM.img --json
```

The install and prepare commands also accept a direct HTTP(S) image URL. The
installer downloads it into its temporary workspace, then applies the same
metadata, profile, capacity, and SHA-256 checks as a local image:

```sh
./onekvm-installer install --url https://mirror.example/onekvm-nanokvm.img
./onekvm-installer prepare --mirror https://mirror.example/onekvm-nanokvm.img
```

`inspect`, `network`, `releases`, and `verify` print machine-readable JSON.
`solution` checks the current device or a local image and reports blocking
issues together with available repairs. In a non-interactive terminal,
`--image` is required. A repair can be inspected without applying it:

```sh
./onekvm-installer solution \
  --image /data/OneKVM.img \
  --repair pro.sd-boot.firmware \
  --json
```

An interactive repair always displays its scope and asks for confirmation
before writing firmware. Use `--lang auto|en|zh|zh-TW` to select the language;
`auto` follows the system locale.

## Development

The project requires Go 1.23 or newer, `make`, and standard Linux shell
utilities. Dependencies are vendored, and the Makefile disables the parent Go
workspace automatically.

```sh
make help       # List targets
make build      # Build for the current architecture
make test       # Run tests and go vet
make check      # Run formatting, shell checks, tests, and vet
make release    # Build Linux riscv64 and arm64 binaries
make package    # Create dist/v<VERSION>/ release assets
```

For a direct cross-build, for example for NanoKVM:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 \
  go build -mod=vendor -trimpath \
  -o dist/onekvm-installer ./cmd/onekvm-installer
```

Machine-specific implementations live under `internal/machines`; shared
installation logic is in `internal/installer`. The `onekvm_test` build tag
enables all machine implementations for host-side tests.

## Release

GitHub Actions runs checks and packages on pushes and pull requests. A `v*`
tag whose version matches `internal/buildinfo/version.go` publishes a Release
containing Linux `riscv64` and `arm64` executables, a separate
`THIRD_PARTY_NOTICES.txt`, `SHA256SUMS`, and `onekvm.sh`. Serving the published `onekvm.sh` as
`https://onekvm.sh` enables the one-line entry point.

The release workflow can also deploy the optional Cloudflare Worker in
[`cloudflare-worker`](cloudflare-worker/README.md). It embeds the verified
`onekvm.sh` asset from each published Release, serves it to `curl`,
and redirects other user agents to the project page. For requests identified by
Cloudflare as originating in China, the returned script uses the Worker as a
fixed proxy for the release checksums and installer binary; those assets are
cached by the release manifest hash and asset URL.

## License

This project is licensed under the GNU General Public License v3.0. See
[`LICENSE`](LICENSE).
