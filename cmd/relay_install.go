package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"text/template"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/relay"
	"github.com/spf13/cobra"
)

func init() {
	relayCmd.AddCommand(relayInstallCmd)
	relayCmd.AddCommand(relayUninstallCmd)
}

var relayInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "注册中继客户端为系统服务（开机自启）",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if cfg.Relay.Server == "" {
			return fmt.Errorf("未配置中继服务器，请先执行 cftunnel relay init")
		}
		if len(cfg.Relay.Rules) == 0 {
			return fmt.Errorf("暂无中继规则，请先执行 cftunnel relay add")
		}
		binPath, err := relay.EnsureFrpc()
		if err != nil {
			return err
		}
		if err := relay.GenerateFrpcConfig(&cfg.Relay); err != nil {
			return err
		}
		switch runtime.GOOS {
		case "darwin":
			return installRelayLaunchd(binPath)
		case "linux":
			return installRelaySystemd(binPath)
		case "windows":
			return installRelayWindows(binPath)
		default:
			return fmt.Errorf("不支持的平台: %s", runtime.GOOS)
		}
	},
}

// ==================== macOS launchd ====================

const relayPlistName = "com.cftunnelX.frpc"

const relayPlistTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinPath}}</string>
        <string>-c</string>
        <string>{{.ConfigPath}}</string>
    </array>
    <key>KeepAlive</key>
    <true/>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
</dict>
</plist>
`

func relayPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/LaunchAgents", relayPlistName+".plist")
}

func installRelayLaunchd(binPath string) error {
	data := map[string]string{
		"Label":      relayPlistName,
		"BinPath":    binPath,
		"ConfigPath": relay.FrpcConfigPath(),
		"LogPath":    relay.LogFilePath(),
	}
	f, err := os.Create(relayPlistPath())
	if err != nil {
		return err
	}
	defer f.Close()
	if err := template.Must(template.New("").Parse(relayPlistTmpl)).Execute(f, data); err != nil {
		return err
	}
	if err := exec.Command("launchctl", "load", relayPlistPath()).Run(); err != nil {
		return fmt.Errorf("launchctl load 失败: %w", err)
	}
	fmt.Printf("✓ 已注册 launchd 服务: %s\n", relayPlistName)
	return nil
}

func uninstallRelayLaunchd() error {
	exec.Command("launchctl", "unload", relayPlistPath()).Run()
	os.Remove(relayPlistPath())
	fmt.Printf("✓ 已卸载 launchd 服务: %s\n", relayPlistName)
	return nil
}

// ==================== Linux systemd ====================

const relayUnitName = "cftunnelX-relay"

func relayUnitPath() string {
	return "/etc/systemd/system/" + relayUnitName + ".service"
}

func installRelaySystemd(binPath string) error {
	unit := fmt.Sprintf(`[Unit]
Description=cftunnel relay (frpc)
After=network.target

[Service]
ExecStart=%s -c %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, binPath, relay.FrpcConfigPath())

	if err := os.WriteFile(relayUnitPath(), []byte(unit), 0644); err != nil {
		return fmt.Errorf("写入 systemd unit 失败: %w", err)
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload 失败: %w", err)
	}
	if err := exec.Command("systemctl", "enable", "--now", relayUnitName).Run(); err != nil {
		return fmt.Errorf("systemctl enable 失败: %w", err)
	}
	fmt.Printf("✓ 已注册 systemd 服务: %s\n", relayUnitName)
	return nil
}

func uninstallRelaySystemd() error {
	exec.Command("systemctl", "disable", "--now", relayUnitName).Run()
	os.Remove(relayUnitPath())
	fmt.Printf("✓ 已卸载 systemd 服务: %s\n", relayUnitName)
	return nil
}

// ==================== Windows sc ====================

const relaySvcName = "cftunnelX-relay"

func installRelayWindows(binPath string) error {
	binArg := fmt.Sprintf(`%s -c %s`, binPath, relay.FrpcConfigPath())
	if err := exec.Command("sc", "create", relaySvcName, "binPath=", binArg, "start=", "auto").Run(); err != nil {
		return fmt.Errorf("创建 Windows 服务失败: %w", err)
	}
	if err := exec.Command("sc", "start", relaySvcName).Run(); err != nil {
		return fmt.Errorf("启动服务失败: %w", err)
	}
	fmt.Printf("✓ 已注册 Windows 服务: %s\n", relaySvcName)
	return nil
}

func uninstallRelayWindows() error {
	exec.Command("sc", "stop", relaySvcName).Run()
	exec.Command("sc", "delete", relaySvcName).Run()
	fmt.Printf("✓ 已卸载 Windows 服务: %s\n", relaySvcName)
	return nil
}

var relayUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "卸载中继客户端系统服务",
	RunE: func(cmd *cobra.Command, args []string) error {
		switch runtime.GOOS {
		case "darwin":
			return uninstallRelayLaunchd()
		case "linux":
			return uninstallRelaySystemd()
		case "windows":
			return uninstallRelayWindows()
		default:
			return fmt.Errorf("不支持的平台: %s", runtime.GOOS)
		}
	},
}
