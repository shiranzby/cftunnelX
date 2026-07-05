package cmd

import (
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/web"
	"github.com/spf13/cobra"
)

var webPort string
var webOpen bool

func init() {
	webCmd.Flags().StringVar(&webPort, "port", "", "Web UI 端口 (默认 7860)")
	webCmd.Flags().BoolVar(&webOpen, "open", true, "自动打开浏览器")
	rootCmd.AddCommand(webCmd)
}

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "启动 Web 管理面板",
	Long: `启动 Web 管理面板，通过浏览器可视化管理 Cloudflare Tunnel。

功能：
  - 通用配置：API Token / Account ID 输入 + 一键测试
  - Cloud 模式：引导式创建隧道、添加路由
  - Relay 模式：中继规则
  - 主题切换：日间 / 夜间 / 跟随系统
  - Web 管理面板：远程穿透 + 账号密码认证

示例：
  cftunnel web              # 默认端口 7860
  cftunnel web --port 8080  # 自定义端口
  cftunnel web --open=false # 不自动打开浏览器`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		port := webPort
		if port == "" {
			port = cfg.WebUI.Port
		}
		if port == "" {
			port = "7860"
		}

		// Only persist the default port. An explicit --port is a temporary runtime override.
		if webPort == "" && cfg.WebUI.Port != port {
			cfg.WebUI.Port = port
			cfg.Save()
		}

		server := web.NewServer(cfg, port, Version)

		if webOpen {
			url := fmt.Sprintf("http://localhost:%s", port)
			go server.OpenBrowser(url)
		}

		return server.Start()
	},
}
