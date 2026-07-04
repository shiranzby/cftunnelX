package cmd

import (
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/relay"
	"github.com/spf13/cobra"
)

func init() {
	relayCmd.AddCommand(relayUpCmd)
}

var relayUpCmd = &cobra.Command{
	Use:   "up",
	Short: "启动中继客户端",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := relay.Start(); err != nil {
			return err
		}
		fmt.Println("提示: frpc 已在后台运行，使用 cftunnel relay logs -f 查看日志")
		return nil
	},
}
