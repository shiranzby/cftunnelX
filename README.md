# cftunnelX

cftunnelX 是一个面向 Cloudflare Tunnel 与自建 Relay 的内网穿透管理工具。项目将 CLI、WebUI、Wails 桌面客户端、Docker 与 OpenWrt 打包方案放在同一个仓库中维护。

## 特性

- Cloudflare Tunnel 管理：创建隧道、添加路由、启动/停止隧道、查看日志。
- Relay 中继管理：frpc/frps 规则、服务器配置、链路检测与服务自启动。
- WebUI 控制台：控制台、隧道路由、中继规则、Web 管理、终端、日志、设置集中管理。
- 桌面客户端：基于 Wails 复用 WebUI，启动时自动拉起同目录 CLI/Web 服务。
- 相对路径数据：配置写入 exe 同级 `config/`，日志写入 exe 同级 `log/`。
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
go build -buildvcs=false -ldflags "-s -w -H=windowsgui" -o dist\cftunnelX-v4.4.exe .
```

Windows CLI：

```powershell
go build -buildvcs=false -ldflags "-s -w" -o dist\cftunnelX-v4.4-cli.exe .
```

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
| `dist/cftunnelX-v4.4.exe` | 浏览器 GUI 双击版 | 启动内置 Web 服务并用系统默认浏览器打开 |
| `desktop-client/build/bin/cftunnelX-desktop.exe` | Wails 桌面客户端 | 原生窗口嵌入 WebUI，并自动拉起同目录 CLI/Web 服务 |
| `dist/cftunnelX-v4.4-windows-portable.zip` | Windows portable 包 | 包含 GUI、CLI、Wails 桌面端、README、LICENSE、依赖文件、`config/` 与 `log/` |

## Docker

```bash
docker build -t cftunnelx:v4.4 .
docker compose up -d
```

默认端口为 `7860`，数据卷映射到 `/app/config` 与 `/app/log`。

## OpenWrt

GitHub Actions 会生成常见架构的 `.ipk`，包括 `x86_64`、`aarch64_generic`、`arm_cortex-a7`、`mipsel_24kc`、`mips_24kc`、`riscv64`。安装后可使用 `/etc/init.d/cftunnelx enable` 管理自启动。

## 数据目录

- 配置目录：程序同级 `config/`
- 日志目录：程序同级 `log/`
- 依赖目录：程序同级 `config/bin/`

## License

MIT
