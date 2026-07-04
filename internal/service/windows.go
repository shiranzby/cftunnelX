//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

type Windows struct{}

const svcName = "cftunnel"

func (w *Windows) Install(binPath, token string) error {
	binArg := fmt.Sprintf(`%s tunnel --protocol http2 run --token %s`, binPath, token)
	if err := hiddenCommand("sc", "create", svcName, "binPath=", binArg, "start=", "auto").Run(); err != nil {
		return fmt.Errorf("创建服务失败: %w", err)
	}
	return hiddenCommand("sc", "start", svcName).Run()
}

func (w *Windows) Uninstall() error {
	hiddenCommand("sc", "stop", svcName).Run()
	return hiddenCommand("sc", "delete", svcName).Run()
}

func (w *Windows) Running() bool {
	out, err := hiddenCommand("sc", "query", svcName).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "RUNNING")
}

func (w *Windows) Installed() bool {
	return hiddenCommand("sc", "query", svcName).Run() == nil
}

func New() Service {
	return &Windows{}
}

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
