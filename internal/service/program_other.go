//go:build !windows && !linux && !darwin

package service

import "fmt"

// 本文件覆盖 Windows / Linux / macOS 之外的平台（如 FreeBSD）。
// 这些平台目前没有实现开机自启动注册，调用方会收到明确错误。

// InstallProgram 在当前平台不可用。
func InstallProgram(p Program) error {
	return fmt.Errorf("当前平台（%s）暂不支持注册开机自启动服务", p.Name)
}

// UninstallProgram 在当前平台不可用。
func UninstallProgram(name string) error {
	return fmt.Errorf("当前平台暂不支持卸载开机自启动服务")
}

// ProgramStatus 在当前平台恒返回未安装。
func ProgramStatus(name string) (bool, bool) {
	return false, false
}

// ServiceMode 返回当前环境的服务管理方式。
func ServiceMode() string {
	return "不支持"
}
