//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// stopChildProcess 优雅终止子进程（Unix: SIGINT）
func stopChildProcess(cmd *exec.Cmd) {
	cmd.Process.Signal(syscall.SIGINT)
}
