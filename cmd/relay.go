package cmd

import "github.com/spf13/cobra"

var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "中继模式 — 自建服务器全协议穿透（TCP/UDP/HTTP/...）",
	Long:  "通过自建中继服务器实现全协议穿透，支持 TCP、UDP、HTTP、HTTPS、STCP 等。\n需要一台公网服务器运行 frps 服务端。",
}

func init() {
	rootCmd.AddCommand(relayCmd)
}
