# cftunnelX

cftunnelX 是一个面向 Cloudflare Tunnel 与自建 Relay 的内网穿透管理工具，合并 CLI、WebUI 与 Wails 桌面客户端到同一个仓库。

## 特性

- Cloudflare Tunnel 管理：创建隧道、添加路由、启动/停止隧道、查看日志。
- Relay 中继管理：frpc/frps 规则、服务器配置、链路检测与服务自启动。
- WebUI 控制台：控制台、隧道路由、中继规则、Web 管理、终端、日志、设置集中管理。
- 桌面客户端：基于 Wails 复用 WebUI，CLI 与 GUI 共存。
- 相对路径数据：所有版本默认在 exe 同级 `config/` 写配置和依赖，在 exe 同级 `log/` 写日志。
- 离线友好：Windows portable 包可内置 `cloudflared.exe`，避免首次启动依赖第三方下载。

## 仓库

- GitHub: https://github.com/shiranzby/cftunnelX
- License: MIT

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

## 安装脚本

Linux / macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/shiranzby/cftunnelX/main/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/shiranzby/cftunnelX/main/install.ps1 | iex
```

## 从源码构建

```bash
go build -buildvcs=false -ldflags "-s -w" -o cftunnelX .
```

Windows 浏览器 GUI 双击版：

```powershell
go build -buildvcs=false -ldflags "-s -w -H=windowsgui" -o dist\cftunnelX-v4.3.exe .
```

Windows CLI：

```powershell
go build -buildvcs=false -ldflags "-s -w" -o dist\cftunnelX-v4.3-cli.exe .
```

## 桌面客户端

```bash
cd desktop-client
npm install --prefix frontend
wails build
```

Wails 输出文件为 `cftunnelX-desktop`，它会在启动时自动拉起同目录下的 `cftunnelX-cli` 或 `cftunnelX` Web 服务。

## Windows 产物区别

| 文件 | 定位 | 行为 |
| --- | --- | --- |
| `dist/cftunnelX-v4.3.exe` | 浏览器 GUI 双击版 | 启动内置 Web 服务并用系统默认浏览器打开 |
| `desktop-client/build/bin/cftunnelX-desktop.exe` | Wails 桌面客户端 | 原生窗口嵌入 WebUI，自动拉起同目录 CLI/Web 服务 |
| `dist/cftunnelX-v4.3-windows-portable.zip` | Windows portable 包 | 包含 GUI、CLI、Wails 桌面端、README、LICENSE、依赖文件和相对路径数据目录 |

## Docker

```bash
docker build -t cftunnelx:v4.3 .
docker compose up -d
```

Relay 服务端示例位于 `docker/relay-server/`。

## 数据目录

- 配置目录：exe 同级 `config/`
- 日志目录：exe 同级 `log/`
- `config/bin/`：`cloudflared`、`frpc`、`frps` 等依赖二进制默认存放目录

## 说明

cftunnelX 是独立项目，当前仓库同时包含 CLI、WebUI、Relay 管理与桌面客户端代码。第三方引擎依赖包括 Cloudflare 官方 `cloudflared` 与 fatedier `frp`。
