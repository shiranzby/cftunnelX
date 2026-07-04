package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
)

//go:embed index.html
var indexHTML []byte

type Server struct {
	cfg     *config.Config
	mux     *http.ServeMux
	srv     *http.Server
	version string
	started time.Time
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

	s.srv = &http.Server{
		Addr:    ":" + port,
		Handler: s.authMiddleware(s.mux),
	}

	return s
}

func (s *Server) registerRoutes() {
	// SPA 主页
	s.mux.HandleFunc("/", s.handleIndex)

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
// 如果端口已被占用（可能是已有实例），检测是否是 cftunnel 自身：
//   - 是 → 打开浏览器复用已有实例，返回 nil（不阻塞）
//   - 否 → 返回错误
func (s *Server) Start() error {
	port := s.srv.Addr
	if strings.HasPrefix(port, ":") {
		port = port[1:]
	}

	// 检测端口是否已被占用
	listener, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		// 端口被占用，检测是否是 cftunnel 自己的实例
		url := "http://localhost:" + port + "/api/version"
		resp, rerr := http.Get(url)
		if rerr == nil {
			defer resp.Body.Close()
			var v map[string]string
			json.NewDecoder(resp.Body).Decode(&v)
			if ver, ok := v["version"]; ok && ver != "" {
				// 已有 cftunnel 实例在运行，直接打开浏览器
				fmt.Printf("检测到已有 cftunnel 实例运行在端口 %s，打开浏览器...\n", port)
				s.OpenBrowser("http://localhost:" + port)
				return nil
			}
		}
		// 端口被其他程序占用
		return fmt.Errorf("端口 %s 已被占用且非 cftunnel 实例: %w", port, err)
	}
	// 关闭检测用的 listener，让 s.srv.ListenAndServe 重新绑定
	listener.Close()

	go func() {
		fmt.Printf("Web UI 启动在 http://localhost:%s\n", port)
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Web UI 启动失败: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\n正在关闭 Web UI...")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
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
