//go:build !windows

package service

import "fmt"

// 以下函数仅 Windows 有真实实现。这里提供占位实现，使调用方
// （internal/web/handlers.go、cmd/relay_install.go）的平台分支代码
// 可以在所有平台上编译通过；运行时不会走到这些分支。

// ConsoleServiceInstall 非 Windows 平台不可用。
func ConsoleServiceInstall(name, binArg string) error {
	return fmt.Errorf("当前平台不支持通过 sc 注册 Windows 服务")
}

// ConsoleServiceUninstall 非 Windows 平台不可用。
func ConsoleServiceUninstall(name string) error {
	return fmt.Errorf("当前平台不支持通过 sc 卸载 Windows 服务")
}

// ConsoleServiceStatus 非 Windows 平台恒返回未安装。
func ConsoleServiceStatus(name string) (installed bool, running bool) {
	return false, false
}
