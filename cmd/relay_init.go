package cmd

import (
	"fmt"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/spf13/cobra"
)

var relayInitServer string
var relayInitToken string

func init() {
	relayInitCmd.Flags().StringVar(&relayInitServer, "server", "", "中继服务器地址 (格式: IP:端口)")
	relayInitCmd.Flags().StringVar(&relayInitToken, "token", "", "鉴权密钥")
	relayInitCmd.MarkFlagRequired("server")
	relayInitCmd.MarkFlagRequired("token")
	relayCmd.AddCommand(relayInitCmd)
}

var relayInitCmd = &cobra.Command{
	Use:   "init",
	Short: "配置中继服务器连接",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg.Relay.Server = relayInitServer
		cfg.Relay.Token = relayInitToken
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("✔ 中继服务器已配置: %s\n", relayInitServer)
		return nil
	},
}
