//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// processRunning 检查进程是否存活（Unix: kill -0）
func processRunning(pid int) bool {
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}

// processKill 终止进程（Unix: SIGINT）
func processKill(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(os.Interrupt)
}

// processTerminateGraceful 平滑终止：先 SIGTERM，宽限期内未退出再 SIGKILL。
//
// 自愈重启必须走这条路，不能直接 SIGKILL：
// 隧道重启会瞬断所有在途连接（含 SSH 路由），SIGTERM 能让 cloudflared
// 有机会注销连接、刷完日志，减少对使用方的冲击。
func processTerminateGraceful(pid int, grace time.Duration) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// 进程可能已消失，交给调用方按"已退出"处理
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !processRunning(pid) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	// 宽限期到仍未退出，强制结束
	_ = proc.Signal(syscall.SIGKILL)
	return nil
}

// configureCommand 让子进程脱离当前会话。
//
// Setsid 使 cloudflared 成为新会话的首进程、且不持有控制终端，
// 因此父进程（CLI / 终端）退出或 SSH 断开时不会再收到 SIGHUP，
// 与 install 生成的 start-tunnel.sh 中显式使用 setsid 的做法保持一致。
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
