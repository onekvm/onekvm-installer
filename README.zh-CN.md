# OneKVM Installer

[English](README.md) | 简体中文

在设备上安装或重装 OneKVM。安装器下载或校验 `.img`，收集首次启动设置，写入对应存储并校验结果。界面语言为 English、简体中文和繁體中文。

## 支持的设备

| 设备 | 架构 | Profile | 安装目标 |
| --- | --- | --- | --- |
| NanoKVM SG200x（Cube、PCIe、Lite） | `riscv64` | `nanokvm` | SD 卡 |
| NanoKVM Pro Desk | `arm64` | `pro-sd` | SD 卡，直接写入 |
| NanoKVM Pro Desk | `arm64` | `pro-emmc` | eMMC，经恢复安装 |

NanoKVM Pro PCIe 还不能安装，仓库里没有对应的设备树和镜像。Pro 的 SD 安装需要匹配的 SPL，写入前会检查。

## 安装

```sh
curl -fsSL onekvm.sh | sh
```

脚本按 `riscv64` 或 `arm64`，从 [最新 Release](https://github.com/onekvm/onekvm-installer/releases/latest) 下载安装包，校验 SHA-256 后启动。

指定镜像：

```sh
curl -fsSL onekvm.sh \
  | sh -s -- install --url 'https://mirror.example/onekvm-nanokvm.img'
```

安装包改从其他地址下载：

```sh
curl -fsSL onekvm.sh \
  | ONEKVM_INSTALLER_RELEASE_URL=https://mirror.example/installer sh
```

设备需要 Linux，以及这些命令：`curl`、`uname`、`mktemp`、`awk`、`sha256sum`、`tar`、`chmod`、`rm`。

## 安装步骤

1. 选择语言。
2. 选择 GitHub 镜像、HTTP(S) 地址或本地 `.img`。
3. 校验元数据、profile、存储类型和 SHA-256。
4. 已安装 OneKVM 时，选择保留配置或全新安装。全新安装会创建管理员并设置时区。
5. 分别设置有线网络和 Wi-Fi。
6. 检查当前设备和所选镜像。
7. 确认目标磁盘。
8. 写入并校验镜像，然后立即或稍后重启。

写入前会显示目标磁盘。写入会清空该磁盘。确认页、JSON 和最后的访问提示不显示 Wi-Fi 密码。

### 重装

| 模式 | 结果 |
| --- | --- |
| 保留系统配置 | 保留账号、密码、MFA、网络、Wi-Fi、SSH 公钥及启动策略、视频、USB 和时区。跳过账号和网络初始化。目标版本不能低于当前版本。 |
| 全新安装 | 重新设置账号、时区和网络。 |

两种模式都会清除已装扩展、扩展数据、扩展登录绑定和个人文件。设置引用的外部文件，例如自定义证书，留在原处。

### 各机型

- NanoKVM 写入 SD 卡。安装器先在内存中准备自身的恢复环境，用镜像里配套的 FIP、内核和环境替换旧引导，然后重启，并从内存写入完整镜像。重装要求镜像 FIP 与设备现有固件一致；从原厂安装使用镜像 FIP，恢复过程保留该 FIP。
- 从原厂系统安装时，Pro Desk 直接写入 SD 镜像，并保留 eMMC 引导固件和 U-Boot 环境。eMMC 镜像先暂存，再重启进入恢复安装。
- Pro 重装当前启动介质，选择对应的 SD 或 eMMC 镜像。U-Boot 及其环境保持不变。
- 恢复环境会再次校验镜像和配置，把完整镜像载入内存，校验写盘结果，再应用配置。
- 镜像带早期显示时，Pro 在屏幕上显示准备、写入、收尾和暂停。否则进度和错误输出到串口。
- 安装完成后 `/boot` 为 128 MiB。

## 命令

直接运行 `onekvm-installer` 会打开向导。下面的命令只读：

```sh
./onekvm-installer inspect
./onekvm-installer network --interface eth0
./onekvm-installer releases
./onekvm-installer releases --profile nanokvm --tag v0.1.0
./onekvm-installer verify --image /data/OneKVM.img --profile nanokvm
./onekvm-installer solution --image /data/OneKVM.img
./onekvm-installer solution --image /data/OneKVM.img --json
```

`install` 和 `prepare` 也可以直接给镜像 URL。文件先下载到临时目录，再按本地镜像同样校验：

```sh
./onekvm-installer install --url https://mirror.example/onekvm-nanokvm.img
./onekvm-installer prepare --mirror https://mirror.example/onekvm-nanokvm.img
```

`inspect`、`network`、`releases` 和 `verify` 输出 JSON。`solution` 检查当前设备或本地镜像，列出阻断问题和可修复项。非交互运行必须带 `--image`。先查看修复方案：

```sh
./onekvm-installer solution \
  --image /data/OneKVM.img \
  --repair pro.sd-boot.firmware \
  --json
```

交互修复会先说明影响范围，写入固件前再确认。`--lang auto|en|zh|zh-TW` 选择语言，`auto` 跟随系统语言。

## 开发

本仓库是独立的 Go 工程。需要 Go 1.23 或更高版本、`make` 和常见 shell 命令。依赖在 `vendor/` 中，Makefile 会关闭上级 Go workspace。

```sh
make help       # 查看目标
make build      # 构建当前架构
make test       # 运行测试和 go vet
make check      # 运行格式、脚本检查、测试和 vet
make release    # 构建 Linux riscv64 和 arm64 二进制
make package    # 生成 dist/v<VERSION>/ 发布文件
```

交叉编译 NanoKVM：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 \
  go build -mod=vendor -trimpath \
  -o dist/onekvm-installer ./cmd/onekvm-installer
```

机型代码在 `internal/machines`，共用安装流程在 `internal/installer`。`onekvm_test` build tag 可在开发机上启用全部机型。

## 发布

GitHub Actions 在推送和 PR 时检查并打包。与 `internal/buildinfo/version.go` 版本一致的 `v*` tag 会发布 Linux `riscv64`、`arm64` 二进制、`THIRD_PARTY_NOTICES.txt`、`SHA256SUMS` 和 `onekvm.sh`。

`https://onekvm.sh` 提供这个脚本。可选的 [Cloudflare Worker](cloudflare-worker/README.md) 把脚本返回给 `curl`，其他客户端重定向到项目主页。Cloudflare 标记为中国的请求，通过 Worker 下载校验文件和安装器二进制。

## 许可证

GNU General Public License v3.0。见 [`LICENSE`](LICENSE)。
