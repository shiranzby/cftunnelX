package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shiranzby/cftunnelX/internal/authproxy"
	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(authproxyCmd)
}

// authproxyCmd 常驻鉴权代理进程（内部命令）。
//
// 不对用户暴露：正常情况下由 cftunnelX up 或 Web 面板自动拉起。
// 单独运行它仅用于排障。
var authproxyCmd = &cobra.Command{
	Use:    "authproxy",
	Short:  "内部：常驻鉴权代理（由 up / 面板自动拉起）",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAuthProxySupervisor()
	},
}

// runAuthProxySupervisor 前台运行鉴权代理，并按配置变化热更新。
//
// 生命周期由外部（up / Web 面板 / 服务管理器）负责，本进程只做两件事：
// 让代理监听正确的端口、随配置变化自动重启对应代理。
// 因此"在界面里改了认证密码"能立即生效，不需要手动重启任何东西。
func runAuthProxySupervisor() error {
	mgr := authproxy.NewManager()
	defer mgr.StopAll()

	apply := func() {
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[authproxy] 读取配置失败: %v\n", err)
			return
		}
		desired, problems := authproxy.DesiredFromConfig(cfg)
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "[authproxy] 提示: %s\n", p)
		}
		if len(desired) == 0 {
			// 鉴权路由被清空 → 停掉全部，避免留下无人需要的监听端口
			if mgr.Active() > 0 {
				mgr.StopAll()
				fmt.Println("[authproxy] 已无鉴权路由，全部代理已停止")
			}
			return
		}
		changed, errs := mgr.Reconcile(desired)
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "[authproxy] %s\n", e)
		}
		if changed {
			fmt.Printf("[authproxy] 代理已就绪，监听端口 %v\n", mgr.Ports())
		}
	}

	apply()
	fmt.Printf("[authproxy] 已启动，当前监听 %v（配置变化会自动生效）\n", mgr.Ports())

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-quit:
			fmt.Println("[authproxy] 收到退出信号，正在关闭...")
			return nil
		case <-ticker.C:
			apply()
		}
	}
}
