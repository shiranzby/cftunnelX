//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const stillActiveExitCode = 259

// processRunning checks the process directly through Win32 APIs so Web UI
// polling never flashes a tasklist.exe console window.
func processRunning(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActiveExitCode
}

// processKill 优雅终止进程（Windows: taskkill 不带 /F 发送关闭信号）
func processKill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Kill(); err != nil {
		return err
	}
	for i := 0; i < 30; i++ {
		if !processRunning(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// processTerminateGraceful 平滑终止。
//
// Windows 没有 SIGTERM 语义，cloudflared 也不处理 CTRL_BREAK 之外的软信号，
// 因此沿用 processKill（TerminateProcess + 轮询）。grace 参数仅为与 Unix
// 实现保持签名一致；这里已经在内部分段等待进程退出。
func processTerminateGraceful(pid int, grace time.Duration) error {
	return processKill(pid)
}
