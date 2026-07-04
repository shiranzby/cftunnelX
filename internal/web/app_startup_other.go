//go:build !windows

package web

import "fmt"

func appStartupStatus() map[string]interface{} {
	return map[string]interface{}{"installed": false, "running": false, "supported": false}
}

func appStartupInstall() error {
	return fmt.Errorf("当前平台暂不支持通过 Web 设置软件开机自启动")
}

func appStartupUninstall() error {
	return fmt.Errorf("当前平台暂不支持通过 Web 关闭软件开机自启动")
}
