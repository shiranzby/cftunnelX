package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/daemon"
	"github.com/shiranzby/cftunnelX/internal/ingress"
	"github.com/shiranzby/cftunnelX/internal/selfupdate"
	"github.com/spf13/cobra"
)

var allowAuthBypass bool

func init() {
	upCmd.Flags().BoolVar(&allowAuthBypass, "allow-auth-bypass", false,
		"排障用：鉴权代理不可用时仍强制启动（认证将不生效，请勿长期使用）")
	rootCmd.AddCommand(upCmd)
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "启动隧道",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		tunnel := cfg.ActiveTunnel()
		if tunnel == nil || tunnel.Token == "" {
			return fmt.Errorf("请先运行 cftunnelX init && cftunnelX create <名称>")
		}

		// 鉴权代理由**独立常驻进程**持有。
		//
		// up 是「启动即退出」的命令：代理若运行在本进程内，命令一退出代理就消失，
		// 而远端 ingress 仍指向它的端口 —— 用户只会看到 502 且不知道原因。
		// 若为了避开这一点而让 ingress 指向源站，认证就被完全绕过了。
		//
		// 顺序不能反：先把代理解析起来，再推送 ingress，
		// 否则会出现「ingress 已指向代理端口、代理却还没起」的窗口。
		if _, err := daemon.StartAuthProxies(cfg); err != nil {
			return fmt.Errorf("鉴权代理未就绪: %w", err)
		}
		if gaps := ingress.CheckAuthProxies(cfg); len(gaps) > 0 {
			if !allowAuthBypass {
				lines := make([]string, 0, len(gaps))
				for _, g := range gaps {
					lines = append(lines, "    - "+g.String())
				}
				return fmt.Errorf(
					"以下路由启用了鉴权，但鉴权代理未在监听：\n%s\n"+
						"  为避免推送一份会绕过认证的 ingress，本次启动已中止。\n"+
						"  可检查：\n"+
						"    - 鉴权代理日志 %s\n"+
						"    - 端口是否被其他程序占用（cftunnelX status 会显示路径信息）\n"+
						"  确需临时跳过认证排障，可加 --allow-auth-bypass",
					strings.Join(lines, "\n"),
					filepath.Join(config.LogDir(), "cftunnelX-authproxy.log"))
			}
			fmt.Fprintln(os.Stderr,
				"警告: 已启用 --allow-auth-bypass，鉴权路由将直接指向源站，认证不会生效")
		}

		// 启动前同步 ingress，确保本地与远端一致。
		// 统一走 internal/ingress —— 这是全项目唯一生成 ingress 的地方，
		// 会为鉴权路由自动换成代理端口。
		if len(tunnel.Routes) > 0 {
			opts := ingress.Options{BypassAuth: allowAuthBypass}
			if err := ingress.PushWith(context.Background(), cfg, tunnel.ID, opts); err != nil {
				fmt.Printf("警告: 同步 ingress 失败: %v（将使用远端现有配置）\n", err)
			} else {
				fmt.Println("ingress 配置已同步")
			}
		}

		// 自动检查更新（非阻塞，仅提示）
		if cfg.SelfUpdate.AutoCheck {
			if latest, err := selfupdate.LatestVersion(); err == nil {
				if latest != "v"+Version && latest != Version {
					fmt.Printf("发现新版本: %s → %s (运行 cftunnelX update 更新)\n", Version, latest)
				}
			}
		}

		if err := daemon.StartTunnel(tunnel.ID, tunnel.Token); err != nil {
			return err
		}
		fmt.Printf("隧道已启动 (PID: %d)，正在确认进程稳定...\n", daemon.TunnelPID(tunnel.ID))

		// 确认进程不会在数秒内退出。cloudflared 若因 token 无效、
		// 出口 7844 被安全组拦截等原因启动失败，会很快消失；不做这个检查
		// 用户只会看到"已启动"，而隧道实际不通。
		if err := daemon.WaitTunnelStable(tunnel.ID, 6*time.Second); err != nil {
			return err
		}
		fmt.Println("隧道已稳定运行")
		return nil
	},
}
