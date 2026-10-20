# OneKVM Installer

[English](README.md) | 简体中文

OneKVM Installer 是使用 Go 和 Bubble Tea 编写的独立安装与恢复工具。它可以
下载或校验 OneKVM `.img` 镜像，收集首次启动配置，将镜像写入正确的存储
介质并校验结果。安装器支持 English、简体中文和繁體中文，目标设备无需预装
Go 或 Python。

> **构建说明：** 本仓库是独立的 Go 工程。正式软件包由 GitHub Actions 构建。

## 支持的设备

| 设备系列 | 架构 | 镜像 profile | 安装目标 |
| --- | --- | --- | --- |
| NanoKVM SG200x（Cube、PCIe、Lite） | `riscv64` | `nanokvm` | SD 卡 |
| NanoKVM Pro Desk | `arm64` | `pro-sd` | 直接写入 SD 卡 |
| NanoKVM Pro Desk | `arm64` | `pro-emmc` | 暂存后进入 eMMC 恢复安装 |

NanoKVM Pro PCIe 目前不可用，因为仓库尚未包含对应的设备树和镜像。Pro SD
路径要求设备具备匹配的 SPL 支持，安装器会在写入前检查。

## 在设备终端安装

以 root 身份运行一键入口：

```sh
curl -fsSL https://onekvm.sh | sh
# 只有 wget 的系统：
wget -O - https://onekvm.sh | sh
```

当前 shell 不是 root 时，可以让管道右侧的 shell 使用 `sudo`：

```sh
curl -fsSL https://onekvm.sh | sudo sh
```

入口脚本会识别 `riscv64`、`arm64` 或 `amd64`，从 [GitHub latest Release](https://github.com/onekvm/onekvm-installer/releases/latest)
下载匹配的软件包，并在启动安装器前校验 SHA-256。也可以在管道右侧指定镜像：

```sh
curl -fsSL https://onekvm.sh \
  | ONEKVM_INSTALLER_RELEASE_URL=https://mirror.example/installer sh
```

设备需要 Linux、`curl` 或 `wget`，以及入口脚本使用的常见命令
（`uname`、`mktemp`、`awk`、`sha256sum`、`tar`、`chmod` 和 `rm`）。实际安装
必须以 root 身份运行。

## 安装流程

向导依次执行以下步骤：

1. 选择界面语言。
2. 选择 GitHub 镜像或本地 `.img` 文件。
3. 校验镜像 metadata、profile、存储类型和 SHA-256。
4. 创建首个管理员并选择时区。
5. 分别复制或配置有线网络和 Wi-Fi。
6. 针对检测到的设备和镜像运行检查及解决指引。
7. 查看目标磁盘并确认写入。
8. 写入并校验镜像，然后立即重启或稍后重启。

安装器会在任何破坏性操作前显示目标磁盘。写入镜像会清除目标介质上的数据。
安装器拒绝覆盖已有的 OneKVM 系统；Wi-Fi 密码不会显示在确认页、JSON 输出或
最后的访问提示中。

NanoKVM 使用经过校验的 SD 卡布局和恢复引导。Pro Desk SD 直接写入，不暂存也
不进入恢复模式；Pro Desk eMMC 会暂存镜像并重启进入恢复安装。Pro SD 流程会
保留 eMMC 引导固件和 U-Boot 环境。安装完成后，`/boot` 使用 128 MiB 布局。

## 命令行检查与诊断

不带命令运行二进制时会打开交互式向导。以下命令均为只读操作，除非另有说明：

```sh
./onekvm-installer inspect
./onekvm-installer network --interface eth0
./onekvm-installer releases
./onekvm-installer releases --profile nanokvm --tag v0.1.0
./onekvm-installer verify --image /data/OneKVM.img --profile nanokvm
./onekvm-installer solution --image /data/OneKVM.img
./onekvm-installer solution --image /data/OneKVM.img --json
```

`inspect`、`network`、`releases` 和 `verify` 输出机器可读的 JSON。
`solution` 检查当前设备或本地镜像，报告阻断问题及可用修复方案。在非交互终端
中必须提供 `--image`。可以先只查看修复方案而不执行写入：

```sh
./onekvm-installer solution \
  --image /data/OneKVM.img \
  --repair pro.sd-boot.firmware \
  --json
```

交互式修复会先显示影响范围，并在写入固件前要求确认。使用
`--lang auto|en|zh|zh-TW` 选择语言；`auto` 会跟随系统 locale。

## 开发

项目需要 Go 1.23 或更高版本、`make` 和常见 Linux shell 工具。依赖已 vendored，
Makefile 会自动关闭上级 Go workspace。

```sh
make help       # 查看目标
make build      # 构建当前架构
make test       # 运行测试和 go vet
make check      # 运行格式、脚本检查、测试和 vet
make release    # 构建 Linux riscv64、arm64 和 amd64 二进制
make package    # 生成 dist/v<VERSION>/ 发布文件
```

例如，为 NanoKVM 直接交叉编译：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 \
  go build -mod=vendor -trimpath \
  -o dist/onekvm-installer ./cmd/onekvm-installer
```

机型实现位于 `internal/machines`，通用安装流程位于 `internal/installer`。
使用 `onekvm_test` build tag 可以在开发机上启用全部机型实现进行测试。

## 发布

GitHub Actions 会在分支更新和 PR 时运行检查并打包。推送与
`internal/buildinfo/version.go` 版本一致的 `v*` tag 后，会发布包含 Linux
`riscv64`、`arm64` 和 `amd64` 压缩包、`SHA256SUMS` 及 `onekvm.sh` 的 Release。
将发布的 `onekvm.sh` 部署为 `https://onekvm.sh` 即可启用一键入口。

仓库还提供可选的 [Cloudflare Worker](cloudflare-worker/README.md)。每次
Release 发布后，工作流会校验并将该 Release 中的 `onekvm.sh` 嵌入 Worker；
`curl` 和 `wget` 会收到脚本，其他 User-Agent 会被 301 重定向到项目主页。

## 许可证

本项目使用 GNU General Public License v3.0，完整条款见 [`LICENSE`](LICENSE)。
