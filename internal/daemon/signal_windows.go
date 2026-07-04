//go:build windows

package daemon

import (
	"os/exec"
	"strconv"
)

// stopChildProcess 优雅终止子进程（Windows: taskkill 发送关闭信号）
func stopChildProcess(cmd *exec.Cmd) {
	exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
