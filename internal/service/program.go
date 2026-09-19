package service

// 服务命名约定（三平台一致）：
//
//	cftunnelx-tunnel  Cloudflare Tunnel（cloudflared）
//	cftunnelx-relay   中继客户端（frpc）
//	cftunnelx-frps    中继服务端（frps）
//	cftunnelx-webui   Web 管理面板（仅 OpenWrt，由 IPK 提供）
//
// 历史名称 cftunnel / cftunnelx / cftunnelX-relay / frps 会在安装与卸载时一并清理，
// 避免新旧服务同时拉起同一个进程。
const (
	TunnelServiceName = "cftunnelx-tunnel"
	RelayServiceName  = "cftunnelx-relay"
	FrpsServiceName   = "cftunnelx-frps"
	WebUIServiceName  = "cftunnelx-webui"
)

// Program 描述一个需要注册为"开机自启动系统服务"的外部程序。
//
// 引入它的目的：隧道（cloudflared）、中继客户端（frpc）、中继服务端（frps）
// 三者的服务注册逻辑此前分散在 cmd/relay_install.go 与 internal/web/handlers.go
// 中各写一份 systemd / launchd / sc 模板，修一处漏两处。
// 现在统一通过 InstallProgram / UninstallProgram / ProgramStatus 走本包实现。
type Program struct {
	// Name 服务名（不含 .service / .plist 等后缀）。
	Name string
	// Path 可执行文件绝对路径。
	Path string
	// Args 启动参数。
	Args []string
	// Env 需要注入的环境变量（可选）。用于避免把密钥放到命令行上。
	Env map[string]string
	// LogDir 服务日志目录（可选，仅部分平台使用）。
	LogDir string
}
