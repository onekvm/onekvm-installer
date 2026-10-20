# OneKVM Installer

用 Go 和 Bubble Tea 编写的 OneKVM 安装器，在设备当前 Linux 系统中完成镜像下载、配置和恢复安装。
支持简体中文、繁體中文和 English，无需预装 Go 或 Python。

## 使用

以 root 身份在设备终端运行（`onekvm.sh` 域名部署后可用）：

```sh
curl -fsSL https://onekvm.sh | sh
# 只有 wget 的系统
wget -O - https://onekvm.sh | sh
```

脚本自动选择 riscv64 / arm64 / amd64，从
[GitHub latest Release](https://github.com/onekvm/onekvm-installer/releases/latest)
下载对应安装器，校验 SHA-256 后运行。也可从 Release 手动下载压缩包。
非 root 用户将末尾的 `sh` 改为 `sudo sh`。

向导依次选择镜像、创建管理员、设置时区、配置有线网络、单独配置 Wi-Fi、确认安装，最后选择立即或稍后重启。
镜像可从 GitHub 获取或选择本地 `.img`，机型、存储类型与完整性都会校验。
写入引导前可用 Ctrl+C 退出。
退出时会显示安装后的访问方式和管理员用户名。

支持 NanoKVM SG200x（Cube / PCIe / Lite）的 SD 卡恢复安装。
新增 Pro Desk 的两条路径：**SD 卡直接写入，不暂存、不进入恢复模式**；eMMC 暂存后重启进入恢复安装。
本地镜像按 metadata、GitHub 镜像按文件名选择介质，写入前显示目标磁盘并要求确认，写后校验。
Pro SD 写卡流程保留 eMMC 引导固件和 U-Boot 环境；SD 优先启动需要设备上的 SPL 支持。
Pro 路径尚未完成实机刷写验收，PCIe 因镜像尚未包含对应 DTB 暂不开放。
安装会清除目标介质的数据，安装后的 `/boot` 为 128 MiB。
已有 OneKVM 的系统请使用系统更新，安装器会拒绝再次安装。

安装器所选语言会同步为设备语言（简体中文、繁体中文或英文），首次安装的小屏提示也使用该语言。
时区默认为 UTC，可输入 IANA 时区（如 `Asia/Shanghai` 或 `Asia/Taipei`），首次启动时一并应用。

有线网络与 Wi-Fi 使用独立页面，分别选择复制现有配置或手动填写；Wi-Fi 也可留到稍后配置。
复制 Wi-Fi 会读取现有 wpa_supplicant 保存的网络，包括未连接的网络、隐藏网络、连接优先级
和自动连接偏好，支持开放网络、WPA 密码及 64 位十六进制 PSK；分别写入 `password` 和 `psk` 字段。确认页仅显示接口和网络名称，
密码不进入 JSON 输出或安装后的访问提示。没有无线网卡时跳过 Wi-Fi 步骤。
不支持的认证配置会提示手动设置，不会当作开放网络导入。

向导先选择或下载镜像并完成校验，再根据镜像类型和安装目标运行当前机型的全部检查 Hook；最终写入确认前再次检查。
提醒可继续，阻断问题或检查失败需要处理并重新检查通过。解决方案可包含机型注册的修复按钮，先检查并展示范围，用户确认后才执行。
也可独立运行 `onekvm-installer solution --image /data/OneKVM.img`，非交互终端会输出文案，`--json` 输出检查结果；交互入口未指定镜像时先填写本地镜像路径。
Pro Desk 仅对 SD 镜像检查 SD 卡是否存在和启动固件是否支持 SD 优先，eMMC 镜像不提示 SD 启动问题。缺少 SD 卡会阻止继续，卡上的旧内容不影响新镜像的检查。SD 优先启动修复使用内置差分包，只更新 128 KiB 主 SPL，并核对保护区域和写后回读；匹配的 SD U-Boot/ATF 从所选镜像校验，未知原固件或镜像不匹配会阻止写入。
`onekvm-installer solution --image /data/OneKVM.img --repair pro.sd-boot.firmware --json` 可只读预检修复方案；实际写入必须在交互页面中另行确认。
机型扩展接口与最终文案见[设备检查与解决指引](docs/solution-hooks.md)。

## 开发

独立 Go 工程，依赖已 vendored，构建和发布不需要 OE。
需要 Go 1.23+、make 和常见 Linux 命令
（curl / wget、tar、gzip、sha256sum、awk、mktemp）。
用 VS Code 打开本仓库并选择 **Reopen in Container**，即可使用预配置的开发环境。

```sh
make help       # 查看命令
make build      # 构建当前架构
make check      # 格式、脚本语法、测试与 vet
make package    # 构建三种 Linux 架构并打包到 dist/v0.1.0/
```

也可直接交叉编译（NanoKVM 用 `riscv64`，Pro 用 `arm64`）：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -mod=vendor -trimpath \
  -o dist/onekvm-installer ./cmd/onekvm-installer
```

每个机型一个 package：`internal/machines/nanokvm`、`internal/machines/nanokvmpro`。
公共流程在 `internal/installer`，机型通过 `Machine` 接口接入。
注册入口使用 Go build constraints：riscv64 只链接 NanoKVM，arm64 只链接 Pro。
`make test` 使用 `onekvm_test` tag 在开发机验证全部机型。

```sh
./dist/onekvm-installer --help
./dist/onekvm-installer --version
./dist/onekvm-installer inspect
./dist/onekvm-installer solution
./dist/onekvm-installer verify --image /data/OneKVM.img --profile nanokvm
sudo ./dist/onekvm-installer install --image /data/OneKVM.img
```

## 发布

GitHub Actions 在分支更新和 PR 时测试、打包并保存 artifact。
推送 `v*` tag 时，核对 tag 与 `internal/buildinfo/version.go` 一致后发布 GitHub Release，
包含三种架构的压缩包、`SHA256SUMS` 和 `onekvm.sh`；正式版本设为 latest。

将 `onekvm.sh` 作为 `https://onekvm.sh` 的响应正文部署即可启用一键入口。
需要使用下载镜像时，可在管道的 `sh` 一侧设置
`ONEKVM_INSTALLER_RELEASE_URL=https://mirror.example/installer`。
该目录须包含相同命名的压缩包与 `SHA256SUMS`。
