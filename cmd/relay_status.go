package cmd

import (
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/relay"
	"github.com/spf13/cobra"
)

func init() {
	relayCmd.AddCommand(relayStatusCmd)
}

var relayStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看中继连接状态",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		fmt.Printf("服务器: %s\n", cfg.Relay.Server)
		if relay.Running() {
			fmt.Printf("状态:   运行中 (PID: %d)\n", relay.PID())
		} else {
			fmt.Println("状态:   未运行")
		}
		fmt.Printf("规则数: %d\n", len(cfg.Relay.Rules))
		return nil
	},
}
