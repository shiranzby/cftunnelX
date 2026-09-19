//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsWSL 判断当前是否运行在 WSL 中。
// 依据 /proc/version 与 /proc/sys/kernel/osrelease 中的 microsoft 标记。
func IsWSL() bool {
	for _, p := range []string{"/proc/version", "/proc/sys/kernel/osrelease"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(data))
		if strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl") {
			return true
		}
	}
	return false
}

// HasSystemd 判断当前系统是否真的在用 systemd 作为 init。
//
// 注意：仅判断 `systemctl` 是否存在是不够的 —— WSL1 与部分容器里
// 可能装了 systemctl 却没有 PID 1 的 systemd，此时 `systemctl enable`
// 会以 "System has not been booted with systemd as init system" 失败。
// 因此优先看 /run/systemd/system 是否存在，其次才回退到命令探测。
func HasSystemd() bool {
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return true
	}
	if IsWSL() {
		// WSL1 恒无 systemd；WSL2 只有在 /etc/wsl.conf 里显式开启才会创建
		// /run/systemd/system，上面已检查过，这里直接判定为否。
		return false
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}

// New 返回当前 Linux 环境适用的服务实现。
//
// 判定顺序：OpenWrt → procd；有 systemd → systemd；否则 → Manual。
// 此前无条件返回 systemd 实现，导致 OpenWrt 与 WSL1 上的服务注册必然失败，
// 且报错是难以理解的 "systemctl: not found"。
func New() Service {
	switch {
	case IsOpenWrt():
		return &Procd{}
	case HasSystemd():
		return &Systemd{}
	default:
		return &Manual{}
	}
}

// ServiceMode 返回当前环境的服务管理方式，便于展示与排障。
func ServiceMode() string {
	switch {
	case IsOpenWrt():
		return "procd (OpenWrt)"
	case HasSystemd():
		return "systemd"
	default:
		if IsWSL() {
			return "无（WSL 未启用 systemd）"
		}
		return "无（未检测到 systemd/procd）"
	}
}

// Manual 用于没有任何服务管理器的环境（WSL1、精简容器等）。
//
// 这类环境无法注册"开机自启服务"，但**仍然可以正常使用工具本身**：
// 手动或脚本后台启动即可，自启交给外层（Windows 任务计划、/etc/wsl.conf 的
// boot.command、容器的 restart 策略等）。因此这里不再直接报错退出，
// 而是写出一个现成的启动脚本，并把可选的挂钩方式讲清楚。
type Manual struct{}

// LaunchScriptPath 返回 Manual 模式生成的启动脚本路径。
const LaunchScriptPath = "/etc/cftunnelx/start-tunnel.sh"

func (m *Manual) Install(binPath, token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("隧道 token 为空，无法生成启动脚本")
	}
	if err := os.MkdirAll(etcDir, 0700); err != nil {
		return fmt.Errorf("创建 %s 失败: %w", etcDir, err)
	}
	script := `#!/bin/sh
# cftunnelX 自动生成：在无 systemd / procd 的环境中后台拉起 Cloudflare 隧道
# 手动启动: sh ` + LaunchScriptPath + `
# 停止:     pkill -f cftunnelX-authproxy ; pkill -f 'cloudflared.*tunnel' || true
set -e
BIN="` + binPath + `"
TOKEN="` + token + `"
LOGDIR=/var/log/cftunnelx
mkdir -p "$LOGDIR"

# 先拉起鉴权代理：远端 ingress 指向的是它的端口，
# 它不在就会出现 502（注意这是安全但不可用，认证并没有被绕过）。
SELF="` + selfPath() + `"
if [ -x "$SELF" ]; then
	if command -v setsid >/dev/null 2>&1; then
		setsid "$SELF" authproxy >>"$LOGDIR/authproxy.log" 2>&1 &
	else
		nohup "$SELF" authproxy >>"$LOGDIR/authproxy.log" 2>&1 &
	fi
fi

# setsid 让进程脱离当前会话，避免 shell 退出时被回收
if command -v setsid >/dev/null 2>&1; then
	setsid "$BIN" tunnel --protocol http2 run --token "$TOKEN" >>"$LOGDIR/tunnel.log" 2>&1 &
else
	nohup "$BIN" tunnel --protocol http2 run --token "$TOKEN" >>"$LOGDIR/tunnel.log" 2>&1 &
fi
echo "已后台启动鉴权代理与隧道，日志目录: $LOGDIR"
`
	if err := os.WriteFile(LaunchScriptPath, []byte(script), 0700); err != nil {
		return fmt.Errorf("写入启动脚本失败: %w", err)
	}

	mode := "未检测到 systemd"
	hook := "/etc/wsl.conf 中加入:\n" +
		"       [boot]\n" +
		"       command = sh " + LaunchScriptPath
	if !IsWSL() {
		hook = "容器场景请使用 restart 策略；或写入 /etc/rc.local（需自行保证被执行）"
	}
	return fmt.Errorf(
		"当前环境没有可用的服务管理器（%s），无法注册开机自启服务。\n"+
			"  但工具本身可正常使用，已为你生成启动脚本: %s\n"+
			"  立即启动: sudo sh %s\n"+
			"  停止:     sudo pkill -f 'cloudflared.*tunnel'\n"+
			"  若需随系统/实例启动，可在外层挂钩:\n"+
			"       %s\n"+
			"  也可以直接使用 Web 面板的\"软件开机自启动\"开关（与平台服务无关）。",
		mode, LaunchScriptPath, LaunchScriptPath, hook)
}

func (m *Manual) Uninstall() error {
	if _, err := os.Stat(LaunchScriptPath); err != nil {
		return nil
	}
	return os.Remove(LaunchScriptPath)
}

// Running 通过进程名判断隧道是否在运行。
func (m *Manual) Running() bool {
	return exec.Command("pidof", "cloudflared").Run() == nil
}

// Installed 表示启动脚本是否已生成。
func (m *Manual) Installed() bool {
	_, err := os.Stat(LaunchScriptPath)
	return err == nil
}

// manualInstallProgram 在无服务管理器的环境注册外部程序：
// 生成同样的 setsid/nohup 启动脚本，并说明局限。
func manualInstallProgram(p Program) error {
	if err := os.MkdirAll(etcDir, 0700); err != nil {
		return fmt.Errorf("创建 %s 失败: %w", etcDir, err)
	}
	path := filepath.Join(etcDir, "start-"+p.Name+".sh")
	args := ""
	for _, a := range p.Args {
		args += " \"" + a + "\""
	}
	script := `#!/bin/sh
# cftunnelX 自动生成：后台启动 ` + p.Name + `
set -e
LOGDIR=` + logDirOrDefault(p.LogDir) + `
mkdir -p "$LOGDIR"
if command -v setsid >/dev/null 2>&1; then
	setsid "` + p.Path + `"` + args + ` >>"$LOGDIR/` + p.Name + `.log" 2>&1 &
else
	nohup "` + p.Path + `"` + args + ` >>"$LOGDIR/` + p.Name + `.log" 2>&1 &
fi
echo "已后台启动 ` + p.Name + `，日志: $LOGDIR/` + p.Name + `.log"
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		return fmt.Errorf("写入启动脚本失败: %w", err)
	}
	return fmt.Errorf(
		"当前环境没有可用的服务管理器，无法注册 %s 为开机自启服务。\n"+
			"  已生成启动脚本: %s\n"+
			"  立即启动: sudo sh %s",
		p.Name, path, path)
}

func logDirOrDefault(dir string) string {
	if dir == "" {
		return "/var/log/cftunnelx"
	}
	return dir
}
