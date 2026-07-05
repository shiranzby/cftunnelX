# cftunnelX v4.4 WebUI 桌面端与 Actions 交付报告

## 修改总结

- 修复 Wails Windows 客户端启动后只显示 `127.0.0.1` 连接拒绝的问题：桌面端启动时会自动拉起同目录 `cftunnelX-cli.exe` 或 `cftunnelX.exe` 的 Web 服务。
- 删除 Wails 桌面端外层顶栏，仅保留嵌入式 WebUI。
- Web 管理卡片新增“重启服务”，端口保存与远程访问同步改为同一次保存逻辑。
- 启动远程访问时会自动检查并补齐 Tunnel、DNS CNAME、服务路由与 ingress 配置。
- 开机自启动运行状态改为检测 `cftunnelX.exe`、`cftunnelX-cli.exe`、`cftunnelX-desktop.exe`。
- Web 服务启动时立即写入日志，记录版本、配置目录、日志目录、本机 IP。
- 控制台自动刷新在输入框聚焦时暂停，避免修改参数时被全页刷新打断。
- 控制台隧道路由与中继规则右上角改为“全部启动/全部停止”二态切换。
- GitHub Actions 增强 Windows/macOS/Linux CLI、Wails 三平台、Docker 多架构、OpenWrt IPK 多架构构建。
- Windows 便携包统一命名为 `cftunnelX-v4.4-windows-portable.zip`。

## WebUI 与交互修复

- 控制台 Web 管理卡片保留“保存 Web”，新增“重启服务”。
- Web 保存请求新增 `port` 字段，避免远程访问创建路由时仍使用旧端口。
- `restartWeb()` 调用 `/api/web/restart` 后自动跳转到新端口。
- 自动刷新策略：当 `input/textarea/select` 获得焦点时跳过控制台自动刷新。

## 桌面端说明

- Wails 客户端路径：`desktop-client/build/bin/cftunnelX-desktop.exe`
- 便携包内路径：`cftunnelX-desktop.exe`
- 桌面端不再显示外层状态栏，不再显示“WebUI 已启动 / 重新连接 / 浏览器打开”。
- 桌面端依赖同目录 CLI/Web 服务文件，便携包已内置 `cftunnelX-cli.exe`。

## Actions 修复说明

- Wails Linux runner 固定为 `ubuntu-22.04`。
- Linux Wails 依赖改为 `build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.0-dev libayatana-appindicator3-dev librsvg2-dev`。
- Docker job 使用 Buildx 构建 `linux/amd64` 与 `linux/arm64`。
- OpenWrt IPK job 覆盖 `x86_64`、`aarch64_generic`、`arm_cortex-a7`、`mipsel_24kc`、`mips_24kc`、`riscv64`。

## Docker / Android / OpenWrt 方案

- Docker：v4.4 已接入 Actions 多架构镜像构建，推送到 GHCR。
- OpenWrt：v4.4 已生成 IPK 打包脚本和 Actions 构建矩阵。
- Android：当前 Go WebUI + Wails 架构不能直接产出 APK。建议 v4.5 新增 Android WebView Shell，后端采用远程连接或 Android 原生服务适配，再用 Gradle Actions 输出多 ABI APK/AAB。

## 本地验证

- `go test ./...`：通过。
- WebUI `<script>` 语法解析：通过。
- `desktop-client/frontend npm run build`：通过。
- `desktop-client go test ./...`：通过。
- `wails build -clean`：通过，生成 Windows 桌面客户端。

## 生成产物

- `dist/cftunnelX-v4.4.exe`
- `dist/cftunnelX-v4.4-cli.exe`
- `dist/cftunnelX-v4.4-linux-amd64`
- `dist/cftunnelX-v4.4-darwin-amd64`
- `dist/cftunnelX-v4.4-darwin-arm64`
- `desktop-client/build/bin/cftunnelX-desktop.exe`
- `dist/cftunnelX-v4.4-windows-portable.zip`
- `cftunnelX-v4.4-source-backup.zip`

## 未解决问题

- macOS/Linux Wails 客户端已在 Actions 中配置构建，但本机 Windows 环境无法实际运行验证。
- Android APK 尚未落地，需要单独客户端壳工程。
- WebUI 内仍存在部分历史页面文案乱码，v4.4 优先修复本次涉及的关键路径与发布文档。

## 下一步建议

- v4.5 建立 Android WebView 客户端工程，并确认后端运行模型。
- 将 WebUI 拆分为组件化前端工程，彻底移除单文件 HTML 历史乱码风险。
- 为 Web 管理远程访问补充更详细的状态诊断：Tunnel 是否存在、DNS 是否存在、ingress 是否已同步。
- OpenWrt 继续补 UCI 配置页面和 luci-app 包。
- Docker 增加健康检查、示例环境变量文件和 compose profile。
