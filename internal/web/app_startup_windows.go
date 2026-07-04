//go:build windows

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const appStartupTaskName = "cftunnel-webui"

func appStartupStatus() map[string]interface{} {
	out, err := hiddenCommand("schtasks", "/Query", "/TN", appStartupTaskName, "/FO", "LIST").Output()
	if err != nil {
		return map[string]interface{}{"installed": false, "running": false}
	}
	running := strings.Contains(string(out), "Status: Running") || strings.Contains(string(out), "状态: 正在运行")
	return map[string]interface{}{"installed": true, "running": running}
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
