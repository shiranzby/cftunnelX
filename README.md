# cftunnelX

cftunnelX 是一个面向 Cloudflare Tunnel 与自建 Relay 的内网穿透管理工具。项目将 CLI、WebUI、Wails 桌面客户端、Docker 与 OpenWrt 打包方案放在同一个仓库中维护。

## 特性

- Cloudflare Tunnel 管理：创建隧道、添加路由、启动/停止隧道、查看日志。
- Relay 中继管理：frpc/frps 规则、服务器配置、链路检测与服务自启动。
- WebUI 控制台：控制台、隧道路由、中继规则、Web 管理、终端、日志、设置集中管理。
- 路由级认证：按路由配置认证用户与密码，由本地鉴权代理接管，并自动把 ingress 指向代理端口。
- 桌面客户端：基于 Wails 复用 WebUI，启动时自动拉起同目录 CLI/Web 服务。
- 跨平台服务：Windows 服务（SCM）、Linux systemd、OpenWrt procd、macOS LaunchDaemon 各自实现；
  没有服务管理器的环境（如 WSL1）会明确提示并生成可手动启动的脚本，而不是静默失败。
- 分层数据目录：便携运行写入程序同级 `config/`、`log/`；安装到系统目录则改用
  `/etc/cftunnelx`、`/var/log/cftunnelx`、`/var/lib/cftunnelx/bin`。详见「数据目录」。
- 内置 cloudflared（可选构建）：加 `-tags bundled_cloudflared` 可把 cloudflared 内嵌进二进制，
  首次使用无需联网下载。
- CI 构建：GitHub Actions 构建 Windows/macOS/Linux CLI、Windows/macOS/Linux Wails、Docker 镜像与 OpenWrt IPK。

## 快速开始

```bash
cftunnelX web
cftunnelX init
cftunnelX create my-tunnel
cftunnelX add my-service 3000 -domain app.example.com
cftunnelX up
```

Relay 示例：

```bash
cftunnelX relay init --server 1.2.3.4:7000 --token your-token
cftunnelX relay add ssh tcp 22 6001
cftunnelX relay up
```

## 从源码构建

```bash
go build -buildvcs=false -ldflags "-s -w" -o cftunnelX .
```

Windows 浏览器 GUI 双击版：

```powershell
go build -buildvcs=false -ldflags "-s -w -H=windowsgui" -o dist\cftunnelX-v4.93.2.exe .
```

Windows CLI：

```powershell
go build -buildvcs=false -ldflags "-s -w" -o dist\cftunnelX-v4.93.2-cli.exe .
```

构建内置 cloudflared 的版本（离线可用，二进制约 36 MB；每个架构需单独准备资源）：

```bash
scripts/fetch-cloudflared.sh linux amd64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags bundled_cloudflared -buildvcs=false -ldflags "-s -w" -o cftunnelX .
```

## Linux / WSL / OpenWrt

服务管理方式会按环境自动选择：

| 环境 | 使用的服务管理 | 开机自启 |
| --- | --- | --- |
| Windows | SCM 服务（cloudflared 官方 `service install`） | `cftunnelX install` |
| Linux（systemd） | systemd 单元 | `cftunnelX install` |
| OpenWrt | procd（`/etc/init.d`） | `cftunnelX install` |
| macOS | LaunchDaemon | `cftunnelX install` |
| 无服务管理器（WSL1、精简容器） | 无 | `cftunnelX install` 会给出明确提示并生成启动脚本，需在外层挂钩（见下） |

其它注意事项：

- **管理面板监听地址按环境判定**：桌面环境为 `127.0.0.1`；无图形界面（服务器、路由器、WSL）为 `0.0.0.0`，
  否则从局域网打不开。可用 `--host` 或配置项 `web_ui.listen` 覆盖。
- **访问控制**：未配置管理账号密码时，只有私有网段来源能访问面板，公网来源会被拒绝；
  开启「远程访问」前必须先设置账号密码。
- **OpenWrt**：MIPS / RISC-V 架构没有 cloudflared 官方构建，请改用中继（relay）模式（frp 覆盖这些架构）。
- **WSL1**：没有 systemd，`cftunnelX install` 不会注册服务，而是生成
  `/etc/cftunnelx/start-tunnel.sh`。开机自启需在 Windows 侧配置（启动目录或计划任务）。
- `cftunnelX status` 会显示当前的路径模式与检测到的服务管理方式，排查环境问题先看它。
- `cftunnelX diagnose` 可检查根证书、出方向连通性、API 凭据（Token / Account ID / 权限分层结论）
  与各路由的本地服务、DNS、HTTPS 可达性。

## 桌面客户端

```bash
cd desktop-client
npm install --prefix frontend
wails build
```

Wails 输出文件为 `desktop-client/build/bin/cftunnelX-desktop.exe`。它是原生窗口客户端，会自动启动同目录下的 `cftunnelX-cli.exe` 或 `cftunnelX.exe` Web 服务，并直接嵌入 WebUI。

## Windows 产物区别

| 文件 | 定位 | 行为 |
| --- | --- | --- |
| `dist/cftunnelX-v4.93.2.exe` | 浏览器 GUI 双击版 | 启动内置 Web 服务并用系统默认浏览器打开 |
| `desktop-client/build/bin/cftunnelX-desktop.exe` | Wails 桌面客户端 | 原生窗口嵌入 WebUI，并自动拉起同目录 CLI/Web 服务 |
| `dist/cftunnelX-v4.93.2-windows-portable.zip` | Windows portable 包 | 包含 GUI、CLI、Wails 桌面端、README、LICENSE、依赖文件、`config/` 与 `log/` |

## Docker

```bash
docker build -t cftunnelx:v4.93.2 .
docker compose up -d
```

默认端口为 `7860`，数据卷映射到 `/app/config` 与 `/app/log`。

## OpenWrt

GitHub Actions 会生成常见架构的 `.ipk`，包括 `x86_64`、`aarch64_generic`、`arm_cortex-a7`、`mipsel_24kc`、`mips_24kc`、`riscv64`。安装后可使用 `/etc/init.d/cftunnelx enable` 管理自启动。

## 数据目录

路径按安装位置自动选择，也可用环境变量 `CFTUNNEL_HOME` 强制指定。

| 模式 | 触发条件 | 配置 | 日志 | 依赖二进制 |
| --- | --- | --- | --- | --- |
| 便携 | 程序同级已有 `config/`，或程序不在系统目录 | `<程序目录>/config` | `<程序目录>/log` | `<程序目录>/config/bin` |
| 系统 | 程序位于 `/usr/bin`、`/usr/local/bin` 等系统目录 | `/etc/cftunnelx` | `/var/log/cftunnelx` | `/var/lib/cftunnelx/bin` |
| 用户 | 系统目录不可写时自动降级 | `~/.config/cftunnelX` | `~/.local/state/cftunnelX/log` | `~/.local/share/cftunnelX/bin` |
| 自定义 | 设置了 `CFTUNNEL_HOME` | `$CFTUNNEL_HOME/config` | `$CFTUNNEL_HOME/log` | `$CFTUNNEL_HOME/bin` |

PID 文件在系统模式下位于 `/run/cftunnelx`。

从旧版本升级时，若新位置还没有 `config.yml`，程序会自动把旧位置（程序同级 `config/`）
的配置迁移过来并打印提示，不会让既有配置凭空消失。

## License

MIT
