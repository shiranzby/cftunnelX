package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/daemon"
	"github.com/shiranzby/cftunnelX/internal/ingress"
)

//go:embed index.html
var indexHTML []byte

//go:embed static/logo.png
var logoPNG []byte

type Server struct {
	cfg     *config.Config
	mux     *http.ServeMux
	srv     *http.Server
	version string
	started time.Time
	// stopCh 在服务关闭时关闭，用于通知后台自愈循环退出
	stopCh chan struct{}
}

// NewServer 创建 Web UI 服务器
func NewServer(cfg *config.Config, port string, version string) *Server {
	s := &Server{
		cfg:     cfg,
		mux:     http.NewServeMux(),
		version: version,
		started: time.Now(),
	}

	// 确保 WebUI 配置有默认值
	if port == "" {
		port = cfg.WebUI.Port
	}
	if port == "" {
		port = "7860"
	}

	s.registerRoutes()
	ensureBootLog(version, port)
	logLine("WebUI 初始化: version=%s port=%s config=%s log=%s", version, port, config.Dir(), config.LogDir())
	ips := localIPs()
	if len(ips) == 0 {
		logLine("网络状态: 未检测到可用本机 IP")
	} else {
		logLine("网络状态: 本机 IP %s", strings.Join(ips, ", "))
	}

	// 监听地址策略（详见 resolveListenHost）：
	// 桌面环境默认只监听回环；无图形界面的 Linux/OpenWrt 默认监听所有网卡，
	// 否则用户从局域网根本打不开管理面板。
	host := resolveListenHost(cfg, port)
	logLine("WebUI 监听地址: %s:%s", host, port)

	s.stopCh = make(chan struct{})

	s.srv = &http.Server{
		Addr:              host + ":" + port,
		Handler:           s.authMiddleware(s.mux),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return s
}

// listenHostOverride 由命令行 --host 传入，优先级最高。
var listenHostOverride string

// SetListenHost 设置监听地址覆盖值（供 cmd/web.go 的 --host 使用）。
func SetListenHost(host string) {
	listenHostOverride = strings.TrimSpace(host)
}

// resolveListenHost 解析实际监听地址。
//
// 优先级：命令行 --host > 配置 web_ui.listen > 自动判定。
// 自动判定：开启远程访问或无图形界面（headless）时监听 0.0.0.0，否则只监听 127.0.0.1。
// OpenWrt / 服务器 / 容器都没有 DISPLAY，必须监听所有网卡才能从局域网访问。
func resolveListenHost(cfg *config.Config, port string) string {
	return resolveListenHostFor(cfg, listenHostOverride, IsHeadless())
}

// resolveListenHostFor 是纯计算版本，便于单元测试。
func resolveListenHostFor(cfg *config.Config, override string, headless bool) string {
	if override != "" {
		return override
	}
	if cfg != nil {
		if listen := strings.TrimSpace(cfg.WebUI.Listen); listen != "" && listen != "auto" {
			return listen
		}
		if cfg.WebUI.RemoteEnabled {
			return "0.0.0.0"
		}
	}
	if headless {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// IsHeadless 判断当前是否处于无图形界面环境。
func IsHeadless() bool {
	return isHeadlessFor(runtime.GOOS, os.Getenv("DISPLAY"), os.Getenv("WAYLAND_DISPLAY"))
}

// isHeadlessFor 判断逻辑：Windows 与 macOS 视为有图形界面；
// 其他系统在既无 X11 也无 Wayland 会话时视为 headless（OpenWrt、服务器、容器）。
func isHeadlessFor(goos, display, waylandDisplay string) bool {
	switch goos {
	case "windows", "darwin":
		return false
	default:
		return display == "" && waylandDisplay == ""
	}
}

func (s *Server) registerRoutes() {
	// SPA 主页
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/assets/logo.png", s.handleLogo)

	// API 路由
	s.mux.HandleFunc("/api/config", s.handleConfig)
	s.mux.HandleFunc("/api/config/test", s.handleConfigTest)
	s.mux.HandleFunc("/api/config/diagnose", s.handleConfigDiagnose)
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/tunnel", s.handleTunnel)
	s.mux.HandleFunc("/api/tunnel/order", s.handleTunnelOrder)
	s.mux.HandleFunc("/api/tunnel/up", s.handleTunnelUp)
	s.mux.HandleFunc("/api/tunnel/down", s.handleTunnelDown)
	s.mux.HandleFunc("/api/routes", s.handleRoutes)
	s.mux.HandleFunc("/api/routes/", s.handleRoutes)
	s.mux.HandleFunc("/api/routes/batch", s.handleRoutesBatch)
	s.mux.HandleFunc("/api/port", s.handlePort)
	// i18n/language 已移除
	s.mux.HandleFunc("/api/quick", s.handleQuick)
	s.mux.HandleFunc("/api/quick/up", s.handleQuickUp)
	s.mux.HandleFunc("/api/quick/down", s.handleQuickDown)
	s.mux.HandleFunc("/api/relay", s.handleRelay)
	s.mux.HandleFunc("/api/relay/rules", s.handleRelayRules)
	s.mux.HandleFunc("/api/relay/rules/", s.handleRelayRules)
	s.mux.HandleFunc("/api/relay/rules/order", s.handleRelayRulesOrder)
	s.mux.HandleFunc("/api/relay/up", s.handleRelayUp)
	s.mux.HandleFunc("/api/relay/down", s.handleRelayDown)
	s.mux.HandleFunc("/api/relay/check", s.handleRelayCheck)
	s.mux.HandleFunc("/api/relay/service", s.handleRelayService)
	s.mux.HandleFunc("/api/relay/server", s.handleRelayServer)
	s.mux.HandleFunc("/api/relay/server/setup", s.handleRelayServerSetup)
	s.mux.HandleFunc("/api/diagnose", s.handleDiagnose)
	s.mux.HandleFunc("/api/webpanel", s.handleWebPanel)
	s.mux.HandleFunc("/api/web/restart", s.handleWebRestart)
	s.mux.HandleFunc("/api/zones", s.handleZones)
	s.mux.HandleFunc("/api/theme", s.handleTheme)
	s.mux.HandleFunc("/api/version", s.handleVersion)
	s.mux.HandleFunc("/api/logs", s.handleLogs)
	s.mux.HandleFunc("/api/app/startup", s.handleAppStartup)
	s.mux.HandleFunc("/api/service", s.handleService)
	s.mux.HandleFunc("/api/terminal/exec", s.handleTerminalExec)
	s.mux.HandleFunc("/api/commands", s.handleCommands)
}

// Start 启动服务器。
// 如果监听地址已被占用（可能是已有实例），检测是否是 cftunnelX 自身：
//   - 是 → 打开浏览器复用已有实例，返回 nil（不阻塞）
//   - 否 → 返回错误
//
// 直接持有 net.Listener 并交给 Serve，不再"先探测再关闭再重新绑定"，
// 从而消除端口被抢占的 TOCTOU 窗口。
func (s *Server) Start() error {
	port := s.listenPort()

	listener, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		if s.reuseRunningInstance(port) {
			return nil
		}
		return fmt.Errorf("端口 %s 已被占用且非 cftunnelX 实例: %w", port, err)
	}

	go func() {
		fmt.Printf("Web UI 启动在 http://%s\n", s.srv.Addr)
		if serveErr := s.srv.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			// 不再使用 log.Fatalf：避免跳过所有 defer 清理直接退出进程
			logLine("Web UI 服务异常退出: %v", serveErr)
		}
	}()

	// 后台自愈：确保鉴权代理常驻进程存活，并在代理端口变化后重新同步 ingress
	s.startAuthProxySupervisor()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\n正在关闭 Web UI...")
	close(s.stopCh)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// startAuthProxySupervisor 周期性确保鉴权代理常驻进程存活。
//
// 为什么面板也要做这件事：代理进程可能被 OOM、被手动 kill、或自身崩溃。
// 它一旦消失而隧道还在运行，远端 ingress 指向的端口就没人监听，
// 用户看到的是 502 且不知道为什么。这里做到两件事：
//  1. 发现代理不在就自动拉起（自愈）
//  2. 代理端口集合发生变化时重新推送 ingress，避免 ingress 指向旧端口
func (s *Server) startAuthProxySupervisor() {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		lastPorts := ""

		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
			}

			cfg, err := config.Load()
			if err != nil {
				continue
			}
			ports := ingress.ProxyPorts(cfg)
			if len(ports) == 0 {
				lastPorts = ""
				continue
			}

			wasRunning := daemon.AuthProxyRunning()
			if _, err := daemon.StartAuthProxies(cfg); err != nil {
				logLine("鉴权代理自愈失败: %v", err)
				continue
			}
			if !wasRunning {
				logLine("检测到鉴权代理未运行，已自动拉起（端口 %v）", ports)
			}

			key := fmt.Sprint(ports)
			if key == lastPorts {
				continue
			}
			lastPorts = key
			if t := cfg.ActiveTunnel(); t != nil && daemon.RunningTunnel(t.ID) {
				if err := ingress.Push(context.Background(), cfg, t.ID); err != nil {
					logLine("鉴权代理端口变化后重新同步 ingress 失败: %v", err)
				} else {
					logLine("鉴权代理端口集合变化，ingress 已重新同步（%v）", ports)
				}
			}
		}
	}()
}

// reuseRunningInstance 探测端口上是否已有 cftunnelX 实例在运行。
// 是则打开浏览器复用，返回 true。
func (s *Server) reuseRunningInstance(port string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var v map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return false
	}
	if v["version"] == "" {
		return false
	}
	fmt.Printf("检测到已有 cftunnelX 实例运行在端口 %s，打开浏览器...\n", port)
	s.OpenBrowser("http://127.0.0.1:" + port)
	return true
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(logoPNG)
}

func (s *Server) listenPort() string {
	if s == nil || s.srv == nil {
		return ""
	}
	port := s.srv.Addr
	if strings.HasPrefix(port, ":") {
		return port[1:]
	}
	return port
}

// --- 工具函数 ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeOK(w http.ResponseWriter, v interface{}) {
	writeJSON(w, 200, v)
}

// reloadCfg 重新加载配置
func (s *Server) reloadCfg() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

// logFilePath 返回日志文件路径
func logFilePath() string {
	return filepath.Join(config.LogDir(), "cftunnelX.log")
}

func ensureBootLog(version, port string) {
	logPath := logFilePath()
	_ = os.MkdirAll(filepath.Dir(logPath), 0700)
	logLine("cftunnelX %s Web 服务启动: port=%s", version, port)
	logLine("配置目录: %s", config.Dir())
	logLine("日志目录: %s", config.LogDir())
}
