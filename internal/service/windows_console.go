//go:build windows

package service

import (
	"fmt"
	"strings"
	"time"
)

// ConsoleServiceInstall 注册控制台程序（frpc / frps 等）为 Windows 服务，
// 并在创建后实测启动结果。
//
// 背景：Windows SCM 要求被拉起的进程实现服务控制协议（StartServiceCtrlDispatcher）。
// 普通控制台程序无法完成该握手，通常以 error 1053（服务未及时响应启动请求）失败。
// 旧实现只做 `sc create` 就返回成功，用户随后才会发现服务起不来。
// 这里改为：创建 → 启动 → 校验状态；失败则回滚并给出可操作的替代方案。
func ConsoleServiceInstall(name, binArg string) error {
	// 清理同名残留，避免 create 报 1073（服务已存在）
	if queryServiceName(name) {
		hiddenCommand("sc", "stop", name).Run()
		hiddenCommand("sc", "delete", name).Run()
	}

	createOut, err := hiddenCommand("sc", "create", name, "binPath=", binArg, "start=", "auto").CombinedOutput()
	if err != nil {
		return fmt.Errorf("创建服务失败: %w (%s)", err, strings.TrimSpace(string(createOut)))
	}

	// 配置崩溃自动恢复（sc create 默认不设置）
	hiddenCommand("sc", "failure", name,
		"reset=", "86400",
		"actions=", "restart/5000/restart/5000/restart/5000").Run()

	startOut, startErr := hiddenCommand("sc", "start", name).CombinedOutput()
	if startErr == nil && waitServiceState(name, "RUNNING", 10*time.Second) {
		return nil
	}

	// 启动失败：回滚，不留下永远启动不了的服务
	hiddenCommand("sc", "delete", name).Run()
	detail := strings.TrimSpace(string(startOut))
	if detail == "" && startErr != nil {
		detail = startErr.Error()
	}
	return fmt.Errorf(
		"服务已创建但无法启动，已回滚删除。\n"+
			"  详情: %s\n"+
			"  原因: Windows 服务要求进程实现服务控制协议(SCM)，普通控制台程序直接注册为服务时\n"+
			"        通常无法完成握手（典型错误 error 1053）。\n"+
			"  替代方案: 使用 NSSM 或 WinSW 包装后注册，例如\n"+
			"    nssm install %s \"<程序绝对路径>\" <参数...>\n"+
			"    nssm start %s",
		detail, name, name)
}

// ConsoleServiceUninstall 停止并删除服务。
func ConsoleServiceUninstall(name string) error {
	hiddenCommand("sc", "stop", name).Run()
	if err := hiddenCommand("sc", "delete", name).Run(); err != nil {
		// delete 对不存在的服务会失败，此时不算错误
		if !queryServiceName(name) {
			return nil
		}
		return err
	}
	return nil
}

// ConsoleServiceStatus 返回服务的安装与运行状态。
func ConsoleServiceStatus(name string) (installed bool, running bool) {
	out, err := hiddenCommand("sc", "query", name).Output()
	if err != nil {
		return false, false
	}
	return true, parseServiceState(string(out)) == "RUNNING"
}

// queryServiceName 判断服务是否存在于 SCM。
func queryServiceName(name string) bool {
	return hiddenCommand("sc", "query", name).Run() == nil
}

// waitServiceState 轮询等待服务进入指定状态。
func waitServiceState(name, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := hiddenCommand("sc", "query", name).Output()
		if err == nil && parseServiceState(string(out)) == want {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}
