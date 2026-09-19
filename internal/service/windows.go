//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

type Windows struct{}

// legacySvcName 是 v4.92 及更早版本用 `sc create` 注册的服务名。
// 新版改用 cloudflared 官方服务入口后，需要能识别并清理它。
const legacySvcName = "cftunnel"

// preferredSvcNames cloudflared 官方服务安装器使用的服务名候选。
var preferredSvcNames = []string{"Cloudflared", "cloudflared"}

// Install 注册开机自启动系统服务。
//
// 关键变更：不再用 `sc create` 直接包装 cloudflared。
// `sc create` 要求目标进程实现 Windows 服务控制协议（StartServiceCtrlDispatcher），
// 普通控制台程序被 SCM 拉起后无法完成握手，会以 error 1053 失败。
// cloudflared 自带官方服务安装入口，会正确注册服务并配置失败恢复策略。
func (w *Windows) Install(binPath, token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("隧道 token 为空，无法注册服务")
	}

	// 清理历史实现留下的服务，避免两个服务同时拉起隧道
	if w.queryService(legacySvcName) {
		hiddenCommand("sc", "stop", legacySvcName).Run()
		hiddenCommand("sc", "delete", legacySvcName).Run()
	}

	out, err := hiddenCommand(binPath, "service", "install", token).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cloudflared service install 失败: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (w *Windows) Uninstall() error {
	var lastErr error
	for _, name := range w.serviceNameCandidates() {
		if !w.queryService(name) {
			continue
		}
		// 优先用 cloudflared 自带的卸载入口
		if out, err := hiddenCommand("cloudflared", "service", "uninstall").CombinedOutput(); err != nil {
			_ = out
			hiddenCommand("sc", "stop", name).Run()
			if delErr := hiddenCommand("sc", "delete", name).Run(); delErr != nil {
				lastErr = delErr
			}
		}
	}
	// 兜底清理历史服务名
	if w.queryService(legacySvcName) {
		hiddenCommand("sc", "stop", legacySvcName).Run()
		if err := hiddenCommand("sc", "delete", legacySvcName).Run(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (w *Windows) Running() bool {
	name, ok := w.resolveServiceName()
	if !ok {
		return false
	}
	out, err := hiddenCommand("sc", "query", name).Output()
	if err != nil {
		return false
	}
	return parseServiceState(string(out)) == "RUNNING"
}

func (w *Windows) Installed() bool {
	_, ok := w.resolveServiceName()
	return ok
}

// serviceNameCandidates 返回所有可能需要处理的服务名。
func (w *Windows) serviceNameCandidates() []string {
	names := append([]string{}, preferredSvcNames...)
	names = append(names, legacySvcName)
	return names
}

// resolveServiceName 定位当前实际存在的 cloudflared 服务名。
// 依次尝试：官方服务名候选 → 注册表扫描 ImagePath 含 cloudflared 的服务 → 历史服务名。
func (w *Windows) resolveServiceName() (string, bool) {
	for _, name := range preferredSvcNames {
		if w.queryService(name) {
			return name, true
		}
	}
	if name, ok := findCloudflaredServiceInRegistry(); ok {
		return name, true
	}
	if w.queryService(legacySvcName) {
		return legacySvcName, true
	}
	return "", false
}

func (w *Windows) queryService(name string) bool {
	return queryServiceName(name)
}

// findCloudflaredServiceInRegistry 通过服务注册表项的 ImagePath 定位 cloudflared 服务。
// 不依赖 sc.exe 的本地化输出，也不依赖服务名。
func findCloudflaredServiceInRegistry() (string, bool) {
	const servicesKey = `SYSTEM\CurrentControlSet\Services`
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, servicesKey,
		registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer root.Close()

	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return "", false
	}
	for _, name := range names {
		sub, err := registry.OpenKey(root, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		imagePath, _, err := sub.GetStringValue("ImagePath")
		sub.Close()
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(imagePath), "cloudflared") {
			return name, true
		}
	}
	return "", false
}

// parseServiceState 从 `sc query` 输出中提取状态名（如 RUNNING / STOPPED）。
// 输出形如 "    STATE              : 4  RUNNING"，状态名本身为英文，不随系统语言变化。
func parseServiceState(out string) string {
	for _, line := range strings.Split(out, "\n") {
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		fields := strings.Fields(line[idx+1:])
		if len(fields) == 0 {
			continue
		}
		state := strings.ToUpper(fields[len(fields)-1])
		switch state {
		case "RUNNING", "STOPPED", "START_PENDING", "STOP_PENDING", "PAUSED":
			return state
		}
	}
	return ""
}

func New() Service {
	return &Windows{}
}

// ServiceMode 返回当前环境的服务管理方式（用于展示与排障）。
func ServiceMode() string {
	return "Windows 服务 (SCM)"
}

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
