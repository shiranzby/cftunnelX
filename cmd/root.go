package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/logutil"
	"github.com/shiranzby/cftunnelX/internal/web"
	"github.com/spf13/cobra"
)

var Version = "v4.6"

var rootCmd = &cobra.Command{
	Use:     "cftunnelX",
	Short:   "Cloudflare Tunnel 与中继穿透管理工具",
	Version: Version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		checkWindowsVersion()
	},
	Run: func(cmd *cobra.Command, args []string) {
		startWebUI("7860", true)
	},
}

func Execute() {
	normalizeSingleDashLongFlags()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func normalizeSingleDashLongFlags() {
	for i, arg := range os.Args {
		switch arg {
		case "-domain":
			os.Args[i] = "--domain"
		}
	}
}

var isWindowsGUI = false

func startWebUI(port string, openBrowser bool) {
	cfg, err := config.Load()
	if err != nil {
		writeLog("加载配置失败: %v", err)
		os.Exit(1)
	}
	if cfg.WebUI.Port != "" {
		port = cfg.WebUI.Port
	}
	if cfg.WebUI.Port != port {
		cfg.WebUI.Port = port
		_ = cfg.Save()
	}

	isWindowsGUI = detectWindowsGUI()
	if isWindowsGUI || config.Portable() {
		redirectLogToFile()
	}

	writeLog("cftunnelX %s 启动中... (GUI模式: %v)", Version, isWindowsGUI)

	server := web.NewServer(cfg, port, Version)
	if openBrowser {
		url := fmt.Sprintf("http://localhost:%s", port)
		go server.OpenBrowser(url)
	}
	if err := server.Start(); err != nil {
		writeLog("Web UI 启动失败: %v", err)
		os.Exit(1)
	}
}

func writeLog(format string, args ...interface{}) {
	logPath := filepath.Join(config.LogDir(), "cftunnelX.log")
	line := logutil.Format("INFO", fmt.Sprintf(format, args...))
	fmt.Fprintln(os.Stderr, line)
	logutil.Append(logPath, line)
}

func redirectLogToFile() {
	logPath := filepath.Join(config.LogDir(), "cftunnelX.log")
	_ = os.MkdirAll(config.LogDir(), 0700)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	os.Stdout = f
	os.Stderr = f
}
