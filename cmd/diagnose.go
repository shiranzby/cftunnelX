package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/daemon"
	"github.com/spf13/cobra"
)

var diagnoseJSON bool

func init() {
	diagnoseCmd.Flags().BoolVar(&diagnoseJSON, "json", false, "JSON 格式输出")
	rootCmd.AddCommand(diagnoseCmd)
}

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose",
	Short: "诊断 Cloud 模式链路连通性",
	Long: "检测运行环境（根证书/出方向网络）、cloudflared 状态、Cloudflare API 凭据、" +
		"以及每条路由的本地服务、DNS 解析与域名可达性。",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// 使用 ActiveRoutes：旧实现读 cfg.Routes，在单隧道配置迁移后恒为空，
		// 会导致 CLI 诊断永远显示"暂无路由需要检测"。
		var routes []daemon.RouteInput
		for _, r := range cfg.ActiveRoutes() {
			routes = append(routes, daemon.RouteInput{
				Name:     r.Name,
				Hostname: r.Hostname,
				Service:  r.Service,
			})
		}

		result := daemon.Diagnose(routes, cfg.Auth.APIToken, cfg.Auth.AccountID)

		if diagnoseJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(result)
		}

		printDiagnose(result)
		return nil
	},
}

func printDiagnose(r daemon.DiagnoseResult) {
	fmt.Println("Cloud 链路诊断")
	fmt.Println("==============")

	// 运行环境
	e := r.Environment
	fmt.Println("运行环境")
	if e.CAOK {
		fmt.Printf("  根证书:   ✓ %s\n", e.CAPath)
	} else {
		fmt.Println("  根证书:   ✗ 缺失")
		fmt.Printf("            %s\n", e.CAHint)
	}
	if e.OutboundOK {
		fmt.Printf("  出方向:   ✓ %s\n", e.OutboundHint)
	} else {
		fmt.Printf("  出方向:   ✗ %s\n", e.OutboundHint)
	}
	fmt.Printf("  路径模式: %s（配置 %s）\n", config.Mode(), config.Dir())
	fmt.Println()

	// cloudflared 状态
	c := r.Cloudflared
	if c.Installed {
		fmt.Printf("cloudflared: ✓ 已安装 %s\n", c.Version)
		fmt.Printf("  路径: %s\n", c.Path)
		if c.Running {
			fmt.Printf("  进程: ✓ 运行中 (PID: %d)\n", c.PID)
		} else {
			fmt.Println("  进程: ✗ 未运行")
		}
	} else {
		fmt.Println("cloudflared: ✗ 未安装")
	}
	if c.Hint != "" {
		fmt.Printf("  提示: %s\n", c.Hint)
	}

	// API 凭据（分层结果：Token → Account → Zone 权限）
	a := r.API
	if a.Reachable {
		fmt.Printf("Cloudflare API: ✓ Token 有效 (%dms)\n", a.LatencyMS)
	} else {
		fmt.Printf("Cloudflare API: ✗ %s\n", a.Err)
	}
	if a.AccountMessage != "" {
		mark := "✓"
		if !a.AccountOK {
			mark = "✗"
		}
		fmt.Printf("  Account ID: %s %s\n", mark, a.AccountMessage)
	}
	if a.ZonesOK {
		fmt.Printf("  域名列表:   ✓ 可管理 %d 个域名\n", a.ZoneCount)
	}
	if a.Hint != "" {
		fmt.Printf("  提示: %s\n", a.Hint)
	}
	fmt.Println()

	if len(r.Routes) == 0 {
		fmt.Println("暂无路由需要检测")
		fmt.Println("提示: 先执行 cftunnelX create <名称> 与 cftunnelX add <服务名> <端口> -domain <域名>")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "路由\t域名\t本地服务\tDNS\tHTTPS")
	fmt.Fprintln(w, "----\t----\t--------\t---\t-----")
	for _, route := range r.Routes {
		local := "✓"
		if !route.LocalOK {
			local = "✗ " + route.LocalErr
		}
		dns := "✓"
		if !route.DNSOK {
			dns = "✗ " + route.DNSErr
		}
		https := "-"
		if route.DNSOK {
			if route.HTTPOK {
				https = fmt.Sprintf("✓ %d", route.HTTPStatus)
			} else {
				https = "✗ " + route.HTTPErr
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			route.Name, route.Hostname, local, dns, https)
	}
	w.Flush()

	// 逐条打印失败原因与建议，避免用户只看到一个"不可达"
	hasDetail := false
	for _, route := range r.Routes {
		lines := make([]string, 0, 3)
		if !route.LocalOK && route.LocalHint != "" {
			lines = append(lines, "  本地服务: "+route.LocalHint)
		}
		if !route.DNSOK && route.DNSHint != "" {
			lines = append(lines, "  DNS:      "+route.DNSHint)
		}
		if route.DNSOK && !route.HTTPOK && route.HTTPHint != "" {
			lines = append(lines, "  HTTPS:    "+route.HTTPHint)
		}
		if len(lines) == 0 {
			continue
		}
		if !hasDetail {
			fmt.Println("\n失败原因")
			hasDetail = true
		}
		fmt.Printf("%s (%s)\n", route.Name, route.Hostname)
		for _, l := range lines {
			fmt.Println(l)
		}
	}

	fmt.Printf("\n结果: %d 条路由, %d 通 / %d 断\n", r.Total, r.Passed, r.Failed)
}
