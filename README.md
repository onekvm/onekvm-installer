# OneKVM Installer

English | [简体中文](README.zh-CN.md)

Install or reinstall OneKVM from the device. The installer downloads or checks
a `.img`, collects first-boot settings, writes the matching storage, and
verifies the result. Languages: English, Simplified Chinese, and Traditional
Chinese.

## Supported devices

| Device | Architecture | Profile | Target |
| --- | --- | --- | --- |
| NanoKVM SG200x (Cube, PCIe, Lite) | `riscv64` | `nanokvm` | SD card |
| NanoKVM Pro Desk | `arm64` | `pro-sd` | SD card, written directly |
| NanoKVM Pro Desk | `arm64` | `pro-emmc` | eMMC, through recovery |

NanoKVM Pro PCIe cannot be installed yet. This repository does not include its
device tree or image. A Pro SD install needs matching SPL support. The
installer checks that before writing.

## Install

```sh
curl -fsSL onekvm.sh | sh
```

The script selects the `riscv64` or `arm64` package from the
[latest Release](https://github.com/onekvm/onekvm-installer/releases/latest),
checks its SHA-256, and starts the installer.

Use a chosen image:

```sh
curl -fsSL onekvm.sh \
  | sh -s -- install --url 'https://mirror.example/onekvm-nanokvm.img'
```

Download the installer package from another host:

```sh
curl -fsSL onekvm.sh \
  | ONEKVM_INSTALLER_RELEASE_URL=https://mirror.example/installer sh
```

The device needs Linux and these commands: `curl`, `uname`, `mktemp`, `awk`,
`sha256sum`, `tar`, `chmod`, and `rm`.

## Installer steps

1. Choose a language.
2. Choose a GitHub image, an HTTP(S) URL, or a local `.img`.
3. Check metadata, profile, storage type, and SHA-256.
4. On an existing OneKVM system, keep the current settings or start fresh.
   A fresh install creates the administrator and sets the time zone.
5. Set Ethernet and Wi-Fi on separate pages.
6. Check the device and the selected image.
7. Confirm the target disk.
8. Write the image, verify it, then reboot now or later.

The target disk is shown before it is erased. Writing clears that disk.
Wi-Fi passwords are omitted from the confirmation page, JSON output, and the
final access summary.

### Reinstall

| Mode | Result |
| --- | --- |
| Keep system settings | Keeps accounts, passwords, MFA, network, Wi-Fi, SSH public keys and their startup policy, video, USB, and time zone. Skips account and network setup. The image must be the same version or newer. |
| Fresh installation | Sets up the account, time zone, and network again. |

Both modes remove installed extensions, extension data, extension login
bindings, and personal files. External files referenced by settings, such as
custom certificates, are left in place.

### Devices

- NanoKVM writes the SD card. Reinstall prepares a recovery system in RAM,
  replaces the old boot files with the image's matching FIP, kernel, and
  environment, reboots, and writes the full image from RAM. The image FIP must
  match the firmware already on the device. Recovery keeps that FIP.
- From the factory system, Pro Desk writes an SD image directly and keeps the
  eMMC boot firmware and U-Boot environment. An eMMC image is staged first.
  The device then reboots into recovery.
- Pro reinstalls the medium it booted from. Choose the matching SD or eMMC
  image. U-Boot and its environment stay unchanged.
- Recovery checks the image and settings again, loads the full image into RAM,
  verifies the written disk, then applies the settings.
- Pro shows preparation, writing, finishing, and paused states on screen when
  the image includes early display support. Otherwise progress and errors go
  to the serial console.
- `/boot` is 128 MiB after installation.

## Commands

`onekvm-installer` with no command opens the wizard. These commands only read:

```sh
./onekvm-installer inspect
./onekvm-installer network --interface eth0
./onekvm-installer releases
./onekvm-installer releases --profile nanokvm --tag v0.1.0
./onekvm-installer verify --image /data/OneKVM.img --profile nanokvm
./onekvm-installer solution --image /data/OneKVM.img
./onekvm-installer solution --image /data/OneKVM.img --json
```

`install` and `prepare` accept an image URL. The file is downloaded to a
temporary directory and checked like a local image:

```sh
./onekvm-installer install --url https://mirror.example/onekvm-nanokvm.img
./onekvm-installer prepare --mirror https://mirror.example/onekvm-nanokvm.img
```

`inspect`, `network`, `releases`, and `verify` print JSON. `solution` checks
the current device or a local image and lists blocking problems and repairs.
A non-interactive run needs `--image`. Preview a repair before writing:

```sh
./onekvm-installer solution \
  --image /data/OneKVM.img \
  --repair pro.sd-boot.firmware \
  --json
```

An interactive repair shows its scope and asks before writing firmware.
`--lang auto|en|zh|zh-TW` selects the language. `auto` follows the system
locale.

## Development

This repository is a standalone Go project. It needs Go 1.23 or newer, `make`,
and a normal Linux shell. Dependencies are vendored. The Makefile disables the
parent Go workspace.

```sh
make help       # List targets
make build      # Build for the current architecture
make test       # Run tests and go vet
make check      # Run formatting, shell checks, tests, and vet
make release    # Build Linux riscv64 and arm64 binaries
make package    # Create dist/v<VERSION>/ release assets
```

Cross-build for NanoKVM:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 \
  go build -mod=vendor -trimpath \
  -o dist/onekvm-installer ./cmd/onekvm-installer
```

Machine code is in `internal/machines`. Shared install code is in
`internal/installer`. The `onekvm_test` build tag enables every machine for
host tests.

## Release

GitHub Actions checks and packages pushes and pull requests. A `v*` tag that
matches `internal/buildinfo/version.go` publishes Linux `riscv64` and `arm64`
binaries, `THIRD_PARTY_NOTICES.txt`, `SHA256SUMS`, and `onekvm.sh`.

`https://onekvm.sh` serves that script. The optional
[Cloudflare Worker](cloudflare-worker/README.md) returns it to `curl` and
redirects other clients to the project page. Requests Cloudflare marks as
China download the checksums and installer binary through the Worker.

## License

GNU General Public License v3.0. See [`LICENSE`](LICENSE).
