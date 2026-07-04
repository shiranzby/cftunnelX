//go:build windows

package web

import (
	"os/exec"
	"syscall"
)

func hiddenCommand(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	configureHiddenCommand(cmd)
	return cmd
}

func configureHiddenCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

func openBrowserURL(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	configureHiddenCommand(cmd)
	return cmd.Start()
}
