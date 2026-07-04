package cmd

import (
	"context"
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/cfapi"
	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(removeCmd)
}

var removeCmd = &cobra.Command{
	Use:   "remove <名称>",
	Short: "删除路由（清理 DNS + ingress）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		route := cfg.FindRoute(name)
		if route == nil {
			return fmt.Errorf("路由 %s 不存在", name)
		}

		client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
		ctx := context.Background()

		// 删除 DNS 记录
		if route.DNSRecordID != "" && route.ZoneID != "" {
			fmt.Printf("正在删除 DNS 记录 %s...\n", route.Hostname)
			if err := client.DeleteDNSRecord(ctx, route.ZoneID, route.DNSRecordID); err != nil {
				fmt.Printf("警告: 删除 DNS 记录失败: %v\n", err)
			}
		}

		cfg.RemoveRoute(name)
		if err := cfg.Save(); err != nil {
			return err
		}

		// 推送 ingress 配置到远端
		fmt.Println("正在同步 ingress 配置...")
		if err := pushIngress(client, ctx, cfg); err != nil {
			fmt.Printf("警告: 推送 ingress 失败: %v\n", err)
		}

		fmt.Printf("路由 %s 已删除\n", name)
		return nil
	},
}
