//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"strconv"
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

func configureCommand(cmd *exec.Cmd) {}
