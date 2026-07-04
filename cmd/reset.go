package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/spf13/cobra"
)

var resetForce bool

func init() {
	resetCmd.Flags().BoolVar(&resetForce, "force", false, "跳过确认")
	rootCmd.AddCommand(resetCmd)
}

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "重置全部本地配置",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !resetForce {
			fmt.Println("即将删除本地配置与日志，此操作不可恢复。")
			fmt.Print("确认重置? (y/N): ")
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(input)) != "y" {
				fmt.Println("已取消")
				return nil
			}
		}

		cfg, _ := config.Load()
		if cfg != nil && cfg.Tunnel.ID != "" {
			destroyForce = true
			if err := destroyCmd.RunE(cmd, nil); err != nil {
				fmt.Printf("警告: 删除隧道失败: %v\n", err)
			}
		}

		if err := os.RemoveAll(config.Dir()); err != nil {
			return fmt.Errorf("清除配置目录失败: %w", err)
		}
		_ = os.RemoveAll(config.LogDir())

		fmt.Printf("已清除配置目录 %s 和日志目录 %s\n", config.Dir(), config.LogDir())
		fmt.Println("重新开始: cftunnelX init")
		return nil
	},
}
