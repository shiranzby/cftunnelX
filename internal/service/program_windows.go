//go:build windows

package service

import (
	"strings"
)

// quoteWindowsArg 为路径/参数加双引号（sc 的 binPath 参数需要）。
func quoteWindowsArg(s string) string {
	return `"` + s + `"`
}

// buildBinArg 按 sc 的要求拼装 binPath 参数。
//
// sc 要求"程序路径 + 参数"整体作为一个参数，且路径含空格时必须带内层引号，
// 例如 binPath= "\"C:\Program Files\x.exe\" -c \"C:\a b\c.toml\""。
// 旧实现未加引号，路径含空格时服务创建会失败或参数被截断。
func buildBinArg(path string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteWindowsArg(path))
	for _, a := range args {
		parts = append(parts, quoteWindowsArg(a))
	}
	return strings.Join(parts, " ")
}

// InstallProgram 把外部程序注册为 Windows 服务。
func InstallProgram(p Program) error {
	return ConsoleServiceInstall(p.Name, buildBinArg(p.Path, p.Args))
}

// UninstallProgram 停止并删除服务。
func UninstallProgram(name string) error {
	return ConsoleServiceUninstall(name)
}

// ProgramStatus 返回服务的安装与运行状态。
func ProgramStatus(name string) (bool, bool) {
	return ConsoleServiceStatus(name)
}
