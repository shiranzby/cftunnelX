package cmd

import (
	"context"
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/cfapi"
	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(createCmd)
}

var createCmd = &cobra.Command{
	Use:   "create <隧道名称>",
	Short: "创建 Cloudflare Tunnel",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.Auth.APIToken == "" {
			return fmt.Errorf("请先运行 cftunnel init 配置认证信息")
		}
		if cfg.Tunnel.ID != "" {
			return fmt.Errorf("已存在隧道 %s (%s)，如需重建请先 cftunnel destroy", cfg.Tunnel.Name, cfg.Tunnel.ID)
		}

		client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
		ctx := context.Background()

		fmt.Println("正在创建隧道...")
		tunnel, err := client.CreateTunnel(ctx, args[0])
		if err != nil {
			return err
		}
		fmt.Printf("隧道已创建: %s (%s)\n", tunnel.Name, tunnel.ID)

		token, err := client.GetTunnelToken(ctx, tunnel.ID)
		if err != nil {
			return err
		}

		cfg.Tunnel = config.TunnelConfig{ID: tunnel.ID, Name: tunnel.Name, Token: token}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println("\n下一步: cftunnel add <服务名> <端口> -domain <域名>")
		return nil
	},
}
