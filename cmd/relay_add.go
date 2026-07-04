package cmd

import (
	"fmt"
	"strconv"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/spf13/cobra"
)

var relayAddProto string
var relayAddLocal int
var relayAddRemote int
var relayAddDomain string

func init() {
	relayAddCmd.Flags().StringVar(&relayAddProto, "proto", "tcp", "协议类型 (tcp/udp/http/https/stcp)")
	relayAddCmd.Flags().IntVar(&relayAddLocal, "local", 0, "本地端口")
	relayAddCmd.Flags().IntVar(&relayAddRemote, "remote", 0, "远程端口")
	relayAddCmd.Flags().StringVar(&relayAddDomain, "domain", "", "自定义域名（HTTP 模式用）")
	relayAddCmd.MarkFlagRequired("local")
	relayCmd.AddCommand(relayAddCmd)
}

var relayAddCmd = &cobra.Command{
	Use:   "add <名称>",
	Short: "添加中继穿透规则",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.Relay.Server == "" {
			return fmt.Errorf("未配置中继服务器，请先执行 cftunnel relay init")
		}
		if cfg.FindRelayRule(name) != nil {
			return fmt.Errorf("规则 %q 已存在", name)
		}

		rule := config.RelayRule{
			Name:       name,
			Proto:      relayAddProto,
			LocalPort:  relayAddLocal,
			RemotePort: relayAddRemote,
			Domain:     relayAddDomain,
		}
		cfg.Relay.Rules = append(cfg.Relay.Rules, rule)
		if err := cfg.Save(); err != nil {
			return err
		}

		desc := fmt.Sprintf("localhost:%d", relayAddLocal)
		if relayAddRemote > 0 {
			desc += " → :" + strconv.Itoa(relayAddRemote)
		}
		fmt.Printf("✔ 规则已添加: %s (%s %s)\n", name, relayAddProto, desc)
		return nil
	},
}
