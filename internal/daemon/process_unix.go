//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
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

// configureCommand 让子进程脱离当前会话。
//
// Setsid 使 cloudflared 成为新会话的首进程、且不持有控制终端，
// 因此父进程（CLI / 终端）退出或 SSH 断开时不会再收到 SIGHUP，
// 与 install 生成的 start-tunnel.sh 中显式使用 setsid 的做法保持一致。
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
