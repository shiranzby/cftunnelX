package cmd

import (
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/web"
	"github.com/spf13/cobra"
)

var webPort string
var webOpen bool
var webHost string

func init() {
	webCmd.Flags().StringVar(&webPort, "port", "", "Web UI 端口 (默认 7860)")
	webCmd.Flags().StringVar(&webHost, "host", "",
		"监听地址（默认自动：桌面环境 127.0.0.1，无图形界面的 Linux/OpenWrt 为 0.0.0.0）")
	webCmd.Flags().BoolVar(&webOpen, "open", true, "自动打开浏览器（无图形界面环境会自动忽略）")
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
		if err := config.Ensure(); err != nil {
			return err
		}
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

		if webHost != "" {
			web.SetListenHost(webHost)
		}

		server := web.NewServer(cfg, port, Version)

		// 无图形界面环境（OpenWrt / 服务器 / 容器）打开浏览器没有意义，
		// 只会产生 xdg-open 缺失的报错噪音
		if webOpen && !web.IsHeadless() {
			url := fmt.Sprintf("http://localhost:%s", port)
			go server.OpenBrowser(url)
		}
		fmt.Println(config.PathsSummary())
		return server.Start()
	},
}
