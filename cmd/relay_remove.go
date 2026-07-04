package cmd

import (
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	relayCmd.AddCommand(relayRemoveCmd)
}

var relayRemoveCmd = &cobra.Command{
	Use:   "remove <名称>",
	Short: "删除中继穿透规则",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if !cfg.RemoveRelayRule(name) {
			return fmt.Errorf("规则 %q 不存在", name)
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("✔ 规则已删除: %s\n", name)
		return nil
	},
}
