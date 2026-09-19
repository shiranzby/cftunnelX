package cmd

import (
	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/daemon"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(downCmd)
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "停止隧道",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// 先停鉴权代理常驻进程，再停隧道。
		// 顺序与 up 相反：避免"隧道还活着但代理已死"的 502 窗口。
		if err := daemon.StopAuthProxies(); err != nil {
			return err
		}

		if id := cfg.ActiveTunnelID(); id != "" && daemon.RunningTunnel(id) {
			return daemon.StopTunnel(id)
		}
		return daemon.Stop()
	},
}
