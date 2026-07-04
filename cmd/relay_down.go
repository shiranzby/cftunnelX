package cmd

import (
	"github.com/shiranzby/cftunnelX/internal/relay"
	"github.com/spf13/cobra"
)

func init() {
	relayCmd.AddCommand(relayDownCmd)
}

var relayDownCmd = &cobra.Command{
	Use:   "down",
	Short: "停止中继客户端",
	RunE: func(cmd *cobra.Command, args []string) error {
		return relay.Stop()
	},
}
