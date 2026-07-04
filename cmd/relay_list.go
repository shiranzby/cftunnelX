package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	relayCmd.AddCommand(relayListCmd)
}

var relayListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出所有中继穿透规则",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if len(cfg.Relay.Rules) == 0 {
			fmt.Println("暂无中继规则")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "名称\t协议\t本地端口\t远程端口\t域名")
		fmt.Fprintln(w, "----\t----\t--------\t--------\t----")
		for _, r := range cfg.Relay.Rules {
			remote := "-"
			if r.RemotePort > 0 {
				remote = fmt.Sprintf("%d", r.RemotePort)
			}
			domain := "-"
			if r.Domain != "" {
				domain = r.Domain
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
				r.Name, r.Proto, r.LocalPort, remote, domain)
		}
		w.Flush()
		return nil
	},
}
