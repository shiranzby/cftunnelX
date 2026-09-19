package cmd

import (
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/daemon"
	"github.com/shiranzby/cftunnelX/internal/service"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "注册为系统服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		tunnel := cfg.ActiveTunnel()
		if tunnel == nil || tunnel.Token == "" {
			// 守卫被触发时也一并告知当前环境的服务管理方式，
			// 否则在无 systemd 的环境（WSL1 / 容器）里，用户只会看到
			// "未配置隧道"，无从得知后续会走降级逻辑。
			return fmt.Errorf("未配置隧道，请先运行 cftunnelX init && cftunnelX create <名称>\n"+
				"  当前环境服务管理方式: %s\n"+
				"  配置好隧道后再次执行本命令，程序会按该环境选择合适的服务方式；\n"+
				"  若没有服务管理器，将改为生成手动启动脚本。",
				service.ServiceMode(),
			)
		}
		binPath, err := daemon.EnsureCloudflared()
		if err != nil {
			return err
		}
		svc := service.New()
		fmt.Printf("服务管理方式: %s\n", service.ServiceMode())
		if err := svc.Install(binPath, tunnel.Token); err != nil {
			return fmt.Errorf("注册服务失败: %w", err)
		}
		fmt.Println("系统服务已注册，隧道将开机自启动")
		return nil
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "卸载系统服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		svc := service.New()
		if err := svc.Uninstall(); err != nil {
			return fmt.Errorf("卸载服务失败: %w", err)
		}
		fmt.Println("系统服务已卸载")
		return nil
	},
}
