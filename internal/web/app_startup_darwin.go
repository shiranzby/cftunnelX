//go:build darwin

package web

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	appStartupLabel = "com.cftunnelX.webui"
	appStartupFile  = "com.cftunnelX.webui.plist"
)

// plistEscape 转义 XML 文本中的特殊字符。
func plistEscape(s string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(s)
}

func appStartupPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", appStartupFile), nil
}

func appStartupStatus() map[string]interface{} {
	status := map[string]interface{}{
		"supported":  true,
		"installed":  false,
		"running":    false,
		"os_trigger": "LaunchAgent (用户登录)",
		"label":      appStartupLabel,
		"path":       "",
	}
	path, err := appStartupPath()
	if err != nil {
		return status
	}
	status["path"] = path
	data, err := os.ReadFile(path)
	if err != nil {
		return status
	}
	status["installed"] = true
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<string>") {
			status["command"] = strings.TrimSuffix(strings.TrimPrefix(trimmed, "<string>"), "</string>")
			break
		}
	}
	// 已加载（launchctl 能查到该 label）视为已生效
	if err := exec.Command("launchctl", "list", appStartupLabel).Run(); err == nil {
		status["running"] = true
	}
	return status
}

func appStartupInstall() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	path, err := appStartupPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	// KeepAlive 必须为 false：程序在检测到已有实例时会主动退出，
	// 若开启 KeepAlive 会导致 launchd 无限重启。
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>` + appStartupLabel + `</string>
    <key>ProgramArguments</key>
    <array>
        <string>` + plistEscape(exe) + `</string>
        <string>web</string>
        <string>--open=false</string>
    </array>
    <key>WorkingDirectory</key>
    <string>` + plistEscape(filepath.Dir(exe)) + `</string>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>ProcessType</key>
    <string>Background</string>
</dict>
</plist>
`
	if err := os.WriteFile(path, []byte(plist), 0644); err != nil {
		return err
	}
	return loadAppStartup(path)
}

// loadAppStartup 优先使用现代的 bootstrap，回退到已废弃的 load。
func loadAppStartup(path string) error {
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if out, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		if out2, err2 := exec.Command("launchctl", "load", "-w", path).CombinedOutput(); err2 != nil {
			return fmt.Errorf("launchctl 加载失败: %w (%s%s)",
				err, strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	return nil
}

func appStartupUninstall() error {
	path, err := appStartupPath()
	if err != nil {
		return err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if err := exec.Command("launchctl", "bootout", domain, path).Run(); err != nil {
		exec.Command("launchctl", "unload", "-w", path).Run()
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
