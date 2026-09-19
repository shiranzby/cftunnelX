//go:build linux

package web

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/shiranzby/cftunnelX/internal/service"
)

const appStartupDesktopFile = "cftunnelX-webui.desktop"
const appStartupUserUnit = "cftunnelX-webui.service"

// 自启动生效方式
const (
	autostartXDG     = "xdg"          // 桌面会话：XDG autostart（用户登录时触发）
	autostartSystemd = "systemd-user" // 无图形会话但有 systemd：用户级 systemd 单元
	autostartNone    = "none"         // 两者都没有（如 WSL1）：本环境无法自动启动
)

// appStartupPath 返回 XDG autostart 描述文件路径。
func appStartupPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "autostart", appStartupDesktopFile), nil
}

// appStartupUnitPath 返回 systemd 用户单元路径。
func appStartupUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", appStartupUserUnit), nil
}

// detectAutostartMode 判断当前环境真正可用的自启动机制。
//
// 这是本文件最关键的判断：在 WSL1、精简容器、纯 SSH 服务器上，
// 写 ~/.config/autostart/*.desktop 是**永远不会被读取**的——没有图形会话，
// 就没有任何程序会扫描该目录。旧实现无条件返回 supported=true +
// os_trigger="XDG autostart"，属于"假装成功"，会误导使用者。
func detectAutostartMode() (mode string, hint string) {
	graphical := os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	if graphical {
		return autostartXDG, "检测到图形会话，使用 XDG autostart（用户登录桌面时触发）"
	}
	if autostartUserSystemdUsable() {
		return autostartSystemd, "无图形会话但 systemd 可用，使用用户级 systemd 单元（开机即启动）"
	}
	return autostartNone,
		"当前环境既没有图形会话（DISPLAY / WAYLAND_DISPLAY 均为空），也没有可用的 systemd，" +
			"因此没有任何机制会读取自启动配置，此开关无法生效。请改用外层手段：" +
			"WSL1 请在 Windows 侧配置（把 `wsl -d <发行版> -u root -- cftunnelX web --open=false` " +
			"放进 Windows 启动目录，或注册为 Windows 计划任务，必要时再配一个看门狗）；" +
			"WSL2 可用 /etc/wsl.conf 的 [boot] 段（该段在 WSL1 上不生效）；" +
			"Docker 容器请使用 restart 策略；" +
			"若希望由 cftunnelX 托管后台进程，请使用「注册服务」（cftunnelX install）"
}

// autostartUserSystemdUsable 判断 `systemctl --user` 是否真的可用。
// 只检查命令存在并不够：没有 user manager 时会报 "Failed to connect to bus"，
// 此时 enable 不会真正生效。
func autostartUserSystemdUsable() bool {
	if !service.HasSystemd() {
		return false
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

// desktopExecQuote 按 Desktop Entry 规范为含空格/特殊字符的参数加引号。
func desktopExecQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\><~|&;$*?#()`") {
		return s
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return `"` + replacer.Replace(s) + `"`
}

// quoteSystemdExec 为 systemd 的 ExecStart= 参数加引号。
func quoteSystemdExec(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

func describeMode(mode string) string {
	switch mode {
	case autostartXDG:
		return "XDG autostart (桌面会话登录时触发)"
	case autostartSystemd:
		return "systemd 用户单元 (开机启动)"
	default:
		return "当前环境不支持自动启动"
	}
}

func appStartupStatus() map[string]interface{} {
	mode, hint := detectAutostartMode()

	status := map[string]interface{}{
		"supported":  mode != autostartNone,
		"effective":  mode != autostartNone,
		"mode":       mode,
		"installed":  false,
		"running":    false,
		"os_trigger": describeMode(mode),
		"path":       "",
		"hint":       hint,
	}

	if mode == autostartSystemd {
		path, err := appStartupUnitPath()
		if err != nil {
			return status
		}
		status["path"] = path
		if _, err := os.Stat(path); err != nil {
			return status
		}
		status["installed"] = true
		status["running"] = exec.Command("systemctl", "--user", "is-enabled", appStartupUserUnit).Run() == nil
		return status
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
	body := string(data)
	status["installed"] = true
	// Hidden=true 表示该自启动项被显式禁用
	status["running"] = !strings.Contains(body, "Hidden=true")
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "Exec=") {
			status["command"] = strings.TrimSpace(strings.TrimPrefix(line, "Exec="))
			break
		}
	}
	return status
}

func appStartupInstall() error {
	mode, hint := detectAutostartMode()

	// 环境不支持时直接拒绝，且**不落盘**。
	// 写一个永远不会被读取的文件只会制造"已配置"的错觉，反而让使用者去
	// 排查那个文件为什么不起作用，增加成本。
	if mode == autostartNone {
		return fmt.Errorf("无法设置开机自启动：%s", hint)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}

	if mode == autostartSystemd {
		path, err := appStartupUnitPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		unit := strings.Join([]string{
			"[Unit]",
			"Description=cftunnelX Web 管理面板",
			"After=network-online.target",
			"",
			"[Service]",
			"Type=simple",
			"ExecStart=" + quoteSystemdExec(exe) + " web --open=false",
			"Restart=on-failure",
			"RestartSec=5",
			"",
			"[Install]",
			"WantedBy=default.target",
			"",
		}, "\n")
		if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
			return err
		}
		if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user daemon-reload 失败: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		if out, err := exec.Command("systemctl", "--user", "enable", "--now", appStartupUserUnit).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user enable 失败: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	// 走到这里说明有图形会话，写 XDG autostart 描述文件即可生效。
	path, err := appStartupPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	body := strings.Join([]string{
		"[Desktop Entry]",
		"Type=Application",
		"Version=1.0",
		"Name=cftunnelX WebUI",
		"Comment=cftunnelX 软件开机自启动（后台运行 Web 管理面板）",
		"Exec=" + desktopExecQuote(exe) + " web --open=false",
		"Terminal=false",
		"Hidden=false",
		"StartupNotify=false",
		"X-GNOME-Autostart-enabled=true",
		"",
	}, "\n")
	return os.WriteFile(path, []byte(body), 0644)
}

func appStartupUninstall() error {
	var firstErr error

	if path, err := appStartupPath(); err == nil {
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			firstErr = rmErr
		}
	}
	if path, err := appStartupUnitPath(); err == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			exec.Command("systemctl", "--user", "disable", "--now", appStartupUserUnit).Run()
			if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
				firstErr = rmErr
			}
			exec.Command("systemctl", "--user", "daemon-reload").Run()
		}
	}
	return firstErr
}
