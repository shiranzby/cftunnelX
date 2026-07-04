//go:build !windows

package web

import (
	"os/exec"
	"runtime"
)

func hiddenCommand(name string, arg ...string) *exec.Cmd {
	return exec.Command(name, arg...)
}

func configureHiddenCommand(cmd *exec.Cmd) {}

func openBrowserURL(url string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}
