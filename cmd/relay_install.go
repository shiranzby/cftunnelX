package cmd

import (
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/relay"
	"github.com/shiranzby/cftunnelX/internal/service"
	"github.com/spf13/cobra"
)

func init() {
	relayCmd.AddCommand(relayInstallCmd)
	relayCmd.AddCommand(relayUninstallCmd)
}

var relayInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "注册中继客户端为系统服务（开机自启）",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.Relay.Server == "" {
			return fmt.Errorf("未配置中继服务器，请先执行 cftunnelX relay init")
		}
		if len(cfg.Relay.Rules) == 0 {
			return fmt.Errorf("暂无中继规则，请先执行 cftunnelX relay add")
		}
		binPath, err := relay.EnsureFrpc()
		if err != nil {
			return err
		}
		if err := relay.GenerateFrpcConfig(&cfg.Relay); err != nil {
			return err
		}
		// 服务注册统一走 internal/service：
		//   Linux  → systemd，OpenWrt → procd，macOS → LaunchDaemon，Windows → 服务
		if err := service.InstallProgram(service.Program{
			Name:   service.RelayServiceName,
			Path:   binPath,
			Args:   []string{"-c", relay.FrpcConfigPath()},
			LogDir: config.LogDir(),
		}); err != nil {
			return err
		}
		fmt.Printf("已注册中继服务: %s\n", service.RelayServiceName)
		fmt.Printf("%s\n", config.PathsSummary())
		return nil
	},
}

var relayUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "卸载中继客户端系统服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := service.UninstallProgram(service.RelayServiceName); err != nil {
			return err
		}
		fmt.Printf("已卸载中继服务: %s\n", service.RelayServiceName)
		return nil
	},
}
