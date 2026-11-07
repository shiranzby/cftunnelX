package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shiranzby/cftunnelX/internal/authproxy"
	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/ingress"
)

// authproxy 常驻进程相关的路径与生命周期管理。

// AuthProxyPIDPath 返回鉴权代理常驻进程的 PID 文件路径。
func AuthProxyPIDPath() string {
	return filepath.Join(config.RunDir(), "authproxy.pid")
}

// AuthProxyRunning 判断常驻鉴权代理进程是否存活。
func AuthProxyRunning() bool {
	pid := readPIDFile(AuthProxyPIDPath())
	return pid > 0 && processRunning(pid)
}

// StartAuthProxies 确保鉴权代理常驻进程已启动，返回本次是否新拉起了进程。
//
// 🔴 为什么代理必须由**独立常驻进程**持有：
//
// 鉴权代理的监听端口会被写进远端 ingress。如果代理运行在 `cftunnelX up`
// 这个进程内，而 up 本身是"启动即退出"的命令，那么命令一退出代理就消失，
// 远端 ingress 却仍指向那个端口 —— 用户看到的是 502，且不知道原因。
// 反过来，如果为了避开这一点而让 ingress 指向源站，认证就被完全绕过了。
//
// 因此代理必须活在独立进程中：CLI、Web 面板、外部守护谁都可以触发它，
// 但它不依赖任何"短命令"的存活。
func StartAuthProxies(cfg *config.Config) (bool, error) {
	ports := ingress.ProxyPorts(cfg)
	if len(ports) == 0 {
		return false, nil
	}
	// 只有「这些端口上确实是**我们的**代理」才算就绪。
	//
	// 🔴 这里曾经用 ingress.AllListening(ports) 判断，即"端口有人在监听就算就绪"。
	// 默认端口取「源站端口 + 1」，极易被别的本地服务占用；那样会误判为就绪、
	// 直接返回，代理从未启动，而上层仍按配置把 ingress 指向那个端口 ——
	// 公网流量被导向无关服务且不经认证（缺陷 CFX-AUTHPROXY-INGRESS-001）。
	// 现在改为带身份校验的探活。
	if len(authproxy.ProbePorts(ports)) == 0 {
		return false, nil
	}

	// 端口确实被非本程序的进程占用时，直接给出确定结论，
	// 不要反复拉起注定失败的子进程（那会刷屏且永远不收敛）。
	if err := checkAuthProxyPortConflicts(ports); err != nil {
		return false, err
	}

	exe, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("无法定位自身可执行文件: %w", err)
	}

	cmd := exec.Command(exe, "authproxy")
	configureCommand(cmd)
	logFile, err := openLogFile("cftunnelX-authproxy.log")
	if err != nil {
		return false, err
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return false, fmt.Errorf("启动鉴权代理进程失败: %w", err)
	}
	// 子进程已持有 fd 副本，父进程侧立即关闭，避免句柄泄漏
	logFile.Close()

	if err := os.MkdirAll(config.RunDir(), 0700); err != nil {
		return false, err
	}
	if err := os.WriteFile(AuthProxyPIDPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		return false, err
	}
	reapProcess(cmd)
	logf("鉴权代理进程已启动 (PID: %d)，期望端口 %v", cmd.Process.Pid, ports)

	// 等代理真正就绪：以身份探活为准，而不是"端口有人监听"
	if err := authproxy.WaitReady(ports, 10*time.Second); err != nil {
		return true, fmt.Errorf("%w，请查看 %s",
			err, filepath.Join(config.LogDir(), "cftunnelX-authproxy.log"))
	}
	logf("鉴权代理就绪（身份已确认）: %v", ports)
	return true, nil
}

// checkAuthProxyPortConflicts 在拉起子进程前预检端口占用。
//
// 错误信息会直接呈现给用户，因此必须写明端口和处理办法 —— 否则用户只会看到
// 「启动失败」，无从下手。本次事故里 10292 条「已自动拉起」却永远起不来，
// 正是因为失败原因被埋在子进程日志里、而子进程根本没被拉起。
func checkAuthProxyPortConflicts(ports []int) error {
	var busy []string
	for _, p := range ports {
		if authproxy.PortAvailable(p) {
			continue
		}
		if authproxy.Probe(p) {
			continue // 是我们的代理，属于正常情况
		}
		busy = append(busy, strconv.Itoa(p))
	}
	if len(busy) == 0 {
		return nil
	}
	return fmt.Errorf(
		"鉴权代理端口 %s 已被其它进程占用（且不是 cftunnelX 自身）。"+
			"请为该路由显式指定空闲端口（auth.listen_port），或关闭该路由的 auth",
		strings.Join(busy, "、"))
}

// StopAuthProxies 停止常驻鉴权代理进程。
func StopAuthProxies() error {
	pid := readPIDFile(AuthProxyPIDPath())
	if pid <= 0 {
		return nil
	}
	err := processKill(pid)
	// 等进程真正退出，避免紧接着重启时端口仍被占用
	for i := 0; i < 30; i++ {
		if !processRunning(pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	os.Remove(AuthProxyPIDPath())
	if err != nil {
		return fmt.Errorf("停止鉴权代理进程失败: %w", err)
	}
	logf("鉴权代理进程已停止 (PID: %d)", pid)
	return nil
}
