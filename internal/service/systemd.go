//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Systemd struct{}

// legacyUnitNames 记录历史实现使用过的单元名，安装/卸载时一并清理，
// 避免新旧单元同时拉起同一个进程。
var legacyUnitNames = map[string][]string{
	TunnelServiceName: {"cftunnel", "cftunnelx"},
	RelayServiceName:  {"cftunnelX-relay"},
	FrpsServiceName:   {"frps"},
	WebUIServiceName:  {"cftunnelX-webui"},
}

// systemdUnit 描述一个待注册的 systemd 单元。
type systemdUnit struct {
	name   string
	desc   string
	exec   []string
	env    map[string]string // 写入 0600 EnvironmentFile，避免密钥出现在命令行
	docURL string
}

func (u systemdUnit) unitPath() string {
	return "/etc/systemd/system/" + u.name + ".service"
}

func (u systemdUnit) envPath() string {
	return filepath.Join(etcDir, u.name+".env")
}

// quoteArgs 为每个参数加引号，避免路径含空格时 systemd 解析失败。
func quoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, `"`+a+`"`)
	}
	return strings.Join(quoted, " ")
}

func (u systemdUnit) render() string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=" + u.desc + "\n")
	if u.docURL != "" {
		b.WriteString("Documentation=" + u.docURL + "\n")
	}
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	if len(u.env) > 0 {
		b.WriteString("EnvironmentFile=" + u.envPath() + "\n")
	}
	b.WriteString("ExecStart=" + quoteArgs(u.exec) + "\n")
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=5\n")
	b.WriteString("LimitNOFILE=65535\n")
	b.WriteString("SyslogIdentifier=" + u.name + "\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=multi-user.target\n")
	return b.String()
}

func (u systemdUnit) write() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("注册系统服务需要 root 权限，请使用 sudo 运行")
	}
	if err := os.MkdirAll(etcDir, 0700); err != nil {
		return fmt.Errorf("创建 %s 失败: %w", etcDir, err)
	}
	if len(u.env) > 0 {
		var b strings.Builder
		for k, v := range u.env {
			b.WriteString(k + "=" + v + "\n")
		}
		if err := os.WriteFile(u.envPath(), []byte(b.String()), 0600); err != nil {
			return fmt.Errorf("写入凭据文件失败: %w", err)
		}
	}
	if err := os.WriteFile(u.unitPath(), []byte(u.render()), 0644); err != nil {
		return fmt.Errorf("写入 systemd 单元失败: %w", err)
	}
	return nil
}

// removeLegacy 清理同义的历史单元。
func (u systemdUnit) removeLegacy() {
	for _, legacy := range legacyUnitNames[u.name] {
		p := "/etc/systemd/system/" + legacy + ".service"
		if _, err := os.Stat(p); err != nil {
			continue
		}
		exec.Command("systemctl", "disable", "--now", legacy).Run()
		os.Remove(p)
	}
}

func (u systemdUnit) enableAndStart() error {
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload 失败: %w", err)
	}
	if out, err := exec.Command("systemctl", "enable", "--now", u.name).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable 失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (u systemdUnit) disableAndRemove() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("卸载系统服务需要 root 权限，请使用 sudo 运行")
	}
	exec.Command("systemctl", "disable", "--now", u.name).Run()
	os.Remove(u.envPath())
	if err := os.Remove(u.unitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	u.removeLegacy()
	exec.Command("systemctl", "daemon-reload").Run()
	return nil
}

func (u systemdUnit) status() (installed, running bool) {
	if _, err := os.Stat(u.unitPath()); err == nil {
		installed = true
	}
	if !installed {
		for _, legacy := range legacyUnitNames[u.name] {
			if _, err := os.Stat("/etc/systemd/system/" + legacy + ".service"); err == nil {
				installed = true
				break
			}
		}
	}
	if installed {
		running = exec.Command("systemctl", "is-active", "--quiet", u.name).Run() == nil
	}
	return installed, running
}

// tunnelUnit 返回隧道服务的单元描述。
// binPath 为空时仅用于查询/卸载场景（不需要可执行路径）。
// tunnelUnit 返回隧道服务的单元描述。
// binPath 为空时仅用于查询/卸载场景（不需要可执行路径）。
//
// ExecStart 用 sh -c 包一层：先把鉴权代理进程拉起来，再 exec cloudflared。
// 否则重启后 ingress 指向的鉴权代理端口没人监听，表现为 502
// （注意这是"安全但不可用"——认证没有被绕过）。
func tunnelUnit(binPath string) systemdUnit {
	return systemdUnit{
		name:   TunnelServiceName,
		desc:   "cftunnelX Cloudflare Tunnel",
		exec:   []string{"/bin/sh", "-c", tunnelWrapperCommand(binPath)},
		docURL: "https://github.com/shiranzby/cftunnelX",
	}
}

// tunnelWrapperCommand 生成"先起鉴权代理、再 exec 隧道"的包装命令。
//
// 用自身绝对路径而不是裸命令名：systemd 的 ExecStart 运行在精简 PATH 下，
// 裸命令名在部分发行版上会找不到。
func tunnelWrapperCommand(binPath string) string {
	self := selfPath()
	return self + " authproxy >>/var/log/cftunnelx/authproxy.log 2>&1 || true; " +
		"exec " + binPath + " tunnel --protocol http2 run"
}

// selfPath 返回当前可执行文件的绝对路径，失败时退回命令名。
func selfPath() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "cftunnelX"
}

// ---- Service 接口实现（cloudflared 隧道）----

func (s *Systemd) Install(binPath, token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("隧道 token 为空，无法注册服务")
	}
	u := tunnelUnit(binPath)
	u.env = map[string]string{"TUNNEL_TOKEN": token}
	if err := u.write(); err != nil {
		return err
	}
	u.removeLegacy()
	return u.enableAndStart()
}

func (s *Systemd) Uninstall() error {
	return tunnelUnit("").disableAndRemove()
}

func (s *Systemd) Running() bool {
	_, running := tunnelUnit("").status()
	return running
}

func (s *Systemd) Installed() bool {
	installed, _ := tunnelUnit("").status()
	return installed
}

// ---- Program 接口的 systemd 实现 ----

func systemdInstallProgram(p Program) error {
	u := systemdUnit{
		name: p.Name,
		desc: "cftunnelX " + p.Name,
		exec: append([]string{p.Path}, p.Args...),
		env:  p.Env,
	}
	if err := u.write(); err != nil {
		return err
	}
	u.removeLegacy()
	return u.enableAndStart()
}

func systemdUninstallProgram(name string) error {
	return systemdUnit{name: name}.disableAndRemove()
}

func systemdProgramStatus(name string) (bool, bool) {
	return systemdUnit{name: name}.status()
}
