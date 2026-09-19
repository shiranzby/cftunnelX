//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// programLabel 返回程序对应的 LaunchDaemon label。
func programLabel(name string) string {
	return "com.cftunnelX." + name
}

func programPlistPath(name string) string {
	return filepath.Join(daemonDir, programLabel(name)+".plist")
}

// InstallProgram 把外部程序注册为系统级 LaunchDaemon。
//
// 使用 /Library/LaunchDaemons（开机启动，不依赖用户登录），
// 而不是 ~/Library/LaunchAgents（仅用户登录后启动）。
func InstallProgram(p Program) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("注册开机服务需要 root 权限，请使用 sudo 运行")
	}
	if err := os.MkdirAll(daemonDir, 0755); err != nil {
		return err
	}
	logDir := p.LogDir
	if logDir == "" {
		logDir = "/var/log/cftunnelx"
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, p.Name+".log")

	var args strings.Builder
	args.WriteString("        <string>" + xmlEscape(p.Path) + "</string>\n")
	for _, a := range p.Args {
		args.WriteString("        <string>" + xmlEscape(a) + "</string>\n")
	}

	var envBlock strings.Builder
	if len(p.Env) > 0 {
		envBlock.WriteString("    <key>EnvironmentVariables</key>\n    <dict>\n")
		for k, v := range p.Env {
			envBlock.WriteString("        <key>" + xmlEscape(k) + "</key>\n")
			envBlock.WriteString("        <string>" + xmlEscape(v) + "</string>\n")
		}
		envBlock.WriteString("    </dict>\n")
	}

	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>` + programLabel(p.Name) + `</string>
    <key>ProgramArguments</key>
    <array>
` + args.String() + `    </array>
` + envBlock.String() + `    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>StandardOutPath</key>
    <string>` + xmlEscape(logPath) + `</string>
    <key>StandardErrorPath</key>
    <string>` + xmlEscape(logPath) + `</string>
</dict>
</plist>
`
	plistPath := programPlistPath(p.Name)
	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		return fmt.Errorf("写入 LaunchDaemon 失败（需要 root）: %w", err)
	}
	return bootstrap(plistPath)
}

// UninstallProgram 卸载 LaunchDaemon。
func UninstallProgram(name string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("卸载开机服务需要 root 权限，请使用 sudo 运行")
	}
	plistPath := programPlistPath(name)
	if _, err := os.Stat(plistPath); err != nil {
		return nil
	}
	label := programLabel(name)
	if _, err := exec.Command("launchctl", "bootout", "system/"+label).CombinedOutput(); err != nil {
		exec.Command("launchctl", "unload", "-w", plistPath).Run()
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ProgramStatus 返回 LaunchDaemon 的安装与运行状态。
func ProgramStatus(name string) (bool, bool) {
	if _, err := os.Stat(programPlistPath(name)); err != nil {
		return false, false
	}
	installed := true
	out, err := exec.Command("launchctl", "list", programLabel(name)).Output()
	if err != nil {
		return installed, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		// launchctl list 输出首列为 PID，"-" 表示当前未运行
		if pid, convErr := strconv.Atoi(fields[0]); convErr == nil && pid > 0 {
			return installed, true
		}
	}
	return installed, false
}
