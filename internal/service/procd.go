//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// etcDir 系统级配置目录，与 config 包的 ModeSystem 路径保持一致。
const etcDir = "/etc/cftunnelx"

// IsOpenWrt 判断当前是否为 OpenWrt 等使用 procd 的嵌入式系统。
func IsOpenWrt() bool {
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		return true
	}
	if _, err := os.Stat("/sbin/procd"); err == nil {
		return true
	}
	// 有 opkg 且没有 systemctl：可判定为嵌入式 OpenWrt 系
	if _, err := exec.LookPath("opkg"); err == nil {
		if _, err := exec.LookPath("systemctl"); err != nil {
			return true
		}
	}
	return false
}

// New / ServiceMode / Manual 见 linux_env.go（那里按 OpenWrt → systemd → 无管理器 依次判定）。

// ---- procd 通用实现 ----

type procdProgram struct {
	name   string
	prog   string
	args   []string
	env    map[string]string
	logDir string
}

func (p procdProgram) initPath() string {
	return "/etc/init.d/" + p.name
}

func (p procdProgram) envPath() string {
	return filepath.Join(etcDir, p.name+".env")
}

// script 生成 procd 兼容的 init 脚本。
// 关键点：
//   - USE_PROCD=1 + procd_open_instance 才能被 procd 监管（否则进程会随
//     init 脚本退出而被回收，表现为"服务启动后立刻停止"）；
//   - stdout/stderr 必须显式打开，否则服务日志全部丢失；
//   - 密钥经 EnvironmentFile 注入，不出现在命令行上。
func (p procdProgram) script() string {
	var envLines strings.Builder
	if len(p.env) > 0 {
		envLines.WriteString(fmt.Sprintf("\t[ -f \"%s\" ] && . \"%s\"\n", p.envPath(), p.envPath()))
	}

	var envParams strings.Builder
	for k := range p.env {
		envParams.WriteString(fmt.Sprintf("\tprocd_set_param env %s=\"$%s\"\n", k, k))
	}

	cmd := "\tprocd_set_param command \"" + p.prog + "\""
	for _, a := range p.args {
		cmd += " \"" + a + "\""
	}

	logDir := p.logDir
	if logDir == "" {
		logDir = "/var/log/cftunnelx"
	}

	return `#!/bin/sh /etc/rc.common
# cftunnelX 自动生成，请勿手工修改
# 由 cftunnelX 注册，卸载请执行 cftunnelX uninstall 或删除本文件

START=95
STOP=10
USE_PROCD=1

start_service() {
	mkdir -p ` + etcDir + ` ` + logDir + `
` + envLines.String() + `
	procd_open_instance
` + cmd + `
` + envParams.String() + `	procd_set_param respawn 3600 5 5
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}
`
}

func (p procdProgram) install() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("注册系统服务需要 root 权限，请使用 sudo 运行")
	}
	if err := os.MkdirAll(etcDir, 0700); err != nil {
		return fmt.Errorf("创建 %s 失败: %w", etcDir, err)
	}
	if len(p.env) > 0 {
		var b strings.Builder
		for k, v := range p.env {
			b.WriteString(k + "=" + v + "\n")
		}
		if err := os.WriteFile(p.envPath(), []byte(b.String()), 0600); err != nil {
			return fmt.Errorf("写入凭据文件失败: %w", err)
		}
	}
	if err := os.WriteFile(p.initPath(), []byte(p.script()), 0755); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", p.initPath(), err)
	}

	// 清理历史命名，避免新旧服务同时拉起同一进程
	for _, legacy := range []string{"cftunnel", "cftunnelX-relay"} {
		if legacy == p.name {
			continue
		}
		legacyPath := "/etc/init.d/" + legacy
		if _, err := os.Stat(legacyPath); err == nil {
			exec.Command(legacyPath, "stop").Run()
			exec.Command(legacyPath, "disable").Run()
			os.Remove(legacyPath)
		}
	}

	if out, err := exec.Command(p.initPath(), "enable").CombinedOutput(); err != nil {
		return fmt.Errorf("启用服务失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command(p.initPath(), "restart").CombinedOutput(); err != nil {
		return fmt.Errorf("启动服务失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (p procdProgram) uninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("卸载系统服务需要 root 权限，请使用 sudo 运行")
	}
	if _, err := os.Stat(p.initPath()); err != nil {
		return nil
	}
	exec.Command(p.initPath(), "stop").Run()
	exec.Command(p.initPath(), "disable").Run()
	os.Remove(p.envPath())
	if err := os.Remove(p.initPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// knownPrograms 把服务名映射到实际进程名，供状态检测回退使用。
var knownPrograms = map[string]string{
	TunnelServiceName: "cloudflared",
	RelayServiceName:  "frpc",
	FrpsServiceName:   "frps",
	WebUIServiceName:  "cftunnelX",
}

func (p procdProgram) status() (installed, running bool) {
	if _, err := os.Stat(p.initPath()); err != nil {
		return false, false
	}
	installed = true
	// rc.common 的 running 子命令依赖 procd 状态
	if err := exec.Command(p.initPath(), "running").Run(); err == nil {
		return true, true
	}
	// 回退：按进程名判断（prog 未知时查已知映射表）
	name := filepath.Base(p.prog)
	if name == "" || name == "unknown" {
		name = knownPrograms[p.name]
	}
	if name == "" {
		return installed, false
	}
	return installed, exec.Command("pidof", name).Run() == nil
}

// Procd 实现 Service 接口（OpenWrt 下的 cloudflared 隧道服务）。
type Procd struct{}

func (p *Procd) program(binPath, token string) procdProgram {
	// 用 sh -c 包一层：先拉起鉴权代理，再 exec 隧道。
	// 否则重启后 ingress 指向的代理端口无人监听，表现为 502。
	return procdProgram{
		name: TunnelServiceName,
		prog: "/bin/sh",
		args: []string{"-c", tunnelWrapperCommand(binPath)},
		env:  map[string]string{"TUNNEL_TOKEN": token},
	}
}

func (p *Procd) Install(binPath, token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("隧道 token 为空，无法注册服务")
	}
	return p.program(binPath, token).install()
}

func (p *Procd) Uninstall() error {
	return p.program("", "").uninstall()
}

func (p *Procd) Running() bool {
	_, running := p.program("", "").status()
	return running
}

func (p *Procd) Installed() bool {
	installed, _ := p.program("", "").status()
	return installed
}

// ---- Program 接口的 Linux 实现 ----

// InstallProgram 把外部程序注册为开机自启动服务。
// OpenWrt 走 procd；有 systemd 走 systemd；都没有（WSL1 / 精简容器）
// 则生成启动脚本并说明局限，而不是抛出一句 "systemctl: not found"。
func InstallProgram(p Program) error {
	switch {
	case IsOpenWrt():
		return procdProgram{
			name:   p.Name,
			prog:   p.Path,
			args:   p.Args,
			env:    p.Env,
			logDir: p.LogDir,
		}.install()
	case HasSystemd():
		return systemdInstallProgram(p)
	default:
		return manualInstallProgram(p)
	}
}

// UninstallProgram 卸载外部程序服务。
func UninstallProgram(name string) error {
	switch {
	case IsOpenWrt():
		return procdProgram{name: name, prog: "unknown"}.uninstall()
	case HasSystemd():
		return systemdUninstallProgram(name)
	default:
		path := filepath.Join(etcDir, "start-"+name+".sh")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
}

// ProgramStatus 返回外部程序服务的安装与运行状态。
func ProgramStatus(name string) (bool, bool) {
	switch {
	case IsOpenWrt():
		return procdProgram{name: name, prog: "unknown"}.status()
	case HasSystemd():
		return systemdProgramStatus(name)
	default:
		if _, err := os.Stat(filepath.Join(etcDir, "start-"+name+".sh")); err != nil {
			return false, false
		}
		return true, exec.Command("pidof", knownPrograms[name]).Run() == nil
	}
}
