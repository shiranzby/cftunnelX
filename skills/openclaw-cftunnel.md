# cftunnelX - Cloudflare Tunnel 管理工具

cftunnelX 是一个集 CLI、WebUI、Relay 管理与桌面客户端于一体的 Cloudflare Tunnel / 内网穿透管理工具。

## 安装

Linux / macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/shiranzby/cftunnelX/main/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/shiranzby/cftunnelX/main/install.ps1 | iex
```

## 快速使用

```bash
cftunnelX web
cftunnelX init
cftunnelX create my-tunnel
cftunnelX add my-service 3000 -domain app.example.com
cftunnelX up
```

## WebUI

默认监听 `http://127.0.0.1:7860`，配置写入程序同级 `config/`，日志写入程序同级 `log/`。

## Relay

```bash
cftunnelX relay init --server 1.2.3.4:7000 --token your-token
cftunnelX relay add ssh tcp 22 6001
cftunnelX relay up
```

## 仓库

https://github.com/shiranzby/cftunnelX
