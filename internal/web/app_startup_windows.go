//go:build windows

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const appStartupTaskName = "cftunnelX-webui"

func appStartupStatus() map[string]interface{} {
	out, err := hiddenCommand("schtasks", "/Query", "/TN", appStartupTaskName, "/FO", "LIST").Output()
	if err != nil {
		return map[string]interface{}{"installed": false, "running": appProcessRunning()}
	}
	return map[string]interface{}{"installed": true, "running": appProcessRunning(), "raw": string(out)}
}

func appStartupInstall() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	task := fmt.Sprintf(`"%s"`, exe)
	_ = hiddenCommand("schtasks", "/Delete", "/TN", appStartupTaskName, "/F").Run()
	return hiddenCommand("schtasks", "/Create", "/TN", appStartupTaskName, "/SC", "ONLOGON", "/TR", task, "/RL", "LIMITED", "/F").Run()
}

func appStartupUninstall() error {
	return hiddenCommand("schtasks", "/Delete", "/TN", appStartupTaskName, "/F").Run()
}

func appProcessRunning() bool {
	out, err := hiddenCommand("tasklist", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	s := strings.ToLower(string(out))
	return strings.Contains(s, "cftunnelx.exe") ||
		strings.Contains(s, "cftunnelx-cli.exe") ||
		strings.Contains(s, "cftunnelx-desktop.exe")
}
