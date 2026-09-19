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

var Version = "v4.93.2"

var rootCmd = &cobra.Command{
	Use:     "cftunnelX",
	Short:   "Cloudflare Tunnel 与中继穿透管理工具",
	Version: Version,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		checkWindowsVersion()
		ensureConfigDirs()
	},
	Run: func(cmd *cobra.Command, args []string) {
		startWebUI("7860", true)
	},
}

// ensureConfigDirs 在命令执行前准备好配置 / 日志 / 依赖目录。
//
// Linux 与 OpenWrt 上数据目录可能位于 /etc、/var/log、/var/lib，
// 需要 root 或预先创建；这里给出包含具体路径与解决方式的中文提示，
// 而不是让后续操作抛出 permission denied。
func ensureConfigDirs() {
	if err := config.Ensure(); err != nil {
		fmt.Fprintln(os.Stderr, "错误: "+err.Error())
		os.Exit(1)
	}
	if from := config.DegradedFrom(); from != "" {
		fmt.Fprintf(os.Stderr, "警告: %s 不可写，已改用用户目录 %s\n", from, config.Dir())
		fmt.Fprintln(os.Stderr, "      如需系统级配置，请使用 sudo 运行，或设置 CFTUNNEL_HOME 指定目录")
	}
	if from := config.MigratedFrom(); from != "" {
		fmt.Fprintf(os.Stderr, "提示: 已将旧配置 %s 迁移到 %s\n", from, config.Path())
	}
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
