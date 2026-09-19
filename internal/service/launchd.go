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

type Launchd struct{}

const plistName = "com.cftunnelX.cloudflared"

// daemonDir 系统级 LaunchDaemon 目录。
// 历史实现写 ~/Library/LaunchAgents（用户级 LaunchAgent），只有用户登录时才会
// 运行，无法满足"开机自启动"；真正的开机服务必须放在 /Library/LaunchDaemons。
const daemonDir = "/Library/LaunchDaemons"

// legacyAgentPath 历史 LaunchAgent 路径，安装/卸载时一并清理。
func legacyAgentPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library/LaunchAgents", plistName+".plist")
}

func (l *Launchd) plistPath() string {
	return filepath.Join(daemonDir, plistName+".plist")
}

const logPath = "/var/log/cftunnelx.log"

// xmlEscape 转义 plist 文本节点中的特殊字符。
// 旧实现直接把路径与 token 插入模板，含 & < > 的取值会破坏 plist。
func xmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}

func (l *Launchd) Install(binPath, token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("隧道 token 为空，无法注册服务")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("注册开机服务需要 root 权限，请使用 sudo 运行")
	}
	if err := os.MkdirAll(daemonDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return err
	}

	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>` + plistName + `</string>
    <key>ProgramArguments</key>
    <array>
        <string>` + xmlEscape(binPath) + `</string>
        <string>tunnel</string>
        <string>--protocol</string>
        <string>http2</string>
        <string>run</string>
        <string>--token</string>
        <string>` + xmlEscape(token) + `</string>
    </array>
    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>StandardOutPath</key>
    <string>` + logPath + `</string>
    <key>StandardErrorPath</key>
    <string>` + logPath + `</string>
</dict>
</plist>
`
	if err := os.WriteFile(l.plistPath(), []byte(plist), 0644); err != nil {
		return fmt.Errorf("写入 LaunchDaemon 失败（需要 root）: %w", err)
	}

	// 清理历史 LaunchAgent，避免同一个隧道被登录项重复拉起
	if legacy := legacyAgentPath(); legacy != "" {
		if _, err := os.Stat(legacy); err == nil {
			exec.Command("launchctl", "bootout", "gui/"+uid(), legacy).Run()
			exec.Command("launchctl", "unload", "-w", legacy).Run()
			os.Remove(legacy)
		}
	}

	return bootstrap(l.plistPath())
}

// bootstrap 优先使用现代 launchctl bootstrap，回退到已废弃的 load。
func bootstrap(plist string) error {
	if out, err := exec.Command("launchctl", "bootstrap", "system", plist).CombinedOutput(); err != nil {
		if out2, err2 := exec.Command("launchctl", "load", "-w", plist).CombinedOutput(); err2 != nil {
			return fmt.Errorf("launchctl 加载失败: %w (%s%s)",
				err, strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	return nil
}

func (l *Launchd) Uninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("卸载开机服务需要 root 权限，请使用 sudo 运行")
	}
	plist := l.plistPath()
	if out, err := exec.Command("launchctl", "bootout", "system/"+plistName).CombinedOutput(); err != nil {
		if _, err2 := os.Stat(plist); err2 == nil {
			// bootout 失败时回退到 unload，仍失败则继续尝试删除文件
			exec.Command("launchctl", "unload", "-w", plist).Run()
			_ = out
		}
	}
	if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l *Launchd) Running() bool {
	// launchctl list 只能说明已加载，这里进一步确认进程存在（PID 列非 "-"）
	out, err := exec.Command("launchctl", "list", plistName).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if pid, convErr := strconv.Atoi(fields[0]); convErr == nil && pid > 0 {
			return true
		}
	}
	return false
}

func (l *Launchd) Installed() bool {
	if _, err := os.Stat(l.plistPath()); err == nil {
		return true
	}
	if legacy := legacyAgentPath(); legacy != "" {
		_, err := os.Stat(legacy)
		return err == nil
	}
	return false
}

// uid 返回当前用户 UID 字符串，用于 gui/<uid> domain。
func uid() string {
	return fmt.Sprintf("%d", os.Getuid())
}

func New() Service {
	return &Launchd{}
}

// ServiceMode 返回当前环境的服务管理方式（用于展示与排障）。
func ServiceMode() string {
	return "launchd"
}
