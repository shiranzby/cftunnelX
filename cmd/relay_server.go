package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/shiranzby/cftunnelX/internal/relay"
	"github.com/spf13/cobra"
)

var relayServerPort int

func init() {
	relayServerInstallCmd.Flags().IntVar(&relayServerPort, "port", 7000, "frps 监听端口")
	relayServerCmd.AddCommand(relayServerInstallCmd)
	relayServerCmd.AddCommand(relayServerUninstallCmd)
	relayServerCmd.AddCommand(relayServerStatusCmd)
	relayCmd.AddCommand(relayServerCmd)
}

var relayServerCmd = &cobra.Command{
	Use:   "server",
	Short: "管理中继服务端（frps）",
}

var relayServerInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "安装并启动 frps 服务端（仅 Linux）",
	RunE:  runRelayServerInstall,
}

var relayServerUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "卸载 frps 服务端",
	RunE:  runRelayServerUninstall,
}

var relayServerStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看 frps 服务端状态",
	RunE:  runRelayServerStatus,
}

func randomToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func runRelayServerInstall(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("服务端安装仅支持 Linux，当前平台: %s", runtime.GOOS)
	}

	// 下载 frps
	binPath, err := relay.EnsureFrps()
	if err != nil {
		return err
	}

	// 复制到 /usr/local/bin
	destBin := "/usr/local/bin/frps"
	input, err := os.ReadFile(binPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(destBin, input, 0755); err != nil {
		return fmt.Errorf("复制 frps 到 %s 失败（需要 sudo？）: %w", destBin, err)
	}

	// 生成 token
	token := randomToken()

	// 写配置文件
	configDir := "/etc/frps"
	os.MkdirAll(configDir, 0755)
	configPath := configDir + "/frps.toml"
	configContent := fmt.Sprintf("bindPort = %d\nauth.token = %q\n", relayServerPort, token)
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}

	// 注册 systemd 服务
	unit := fmt.Sprintf(`[Unit]
Description=frps relay server (cftunnel)
After=network.target

[Service]
ExecStart=%s -c %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, destBin, configPath)

	unitPath := "/etc/systemd/system/frps.service"
	if err := os.WriteFile(unitPath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("写入 systemd unit 失败: %w", err)
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemctl daemon-reload 失败: %w", err)
	}
	if err := exec.Command("systemctl", "enable", "--now", "frps").Run(); err != nil {
		return fmt.Errorf("启动 frps 服务失败: %w", err)
	}

	fmt.Println("✓ frps 已安装到", destBin)
	fmt.Println("✓ 配置文件:", configPath)
	fmt.Printf("✓ systemd 服务已启动 (端口 %d)\n", relayServerPort)
	fmt.Println()
	fmt.Println("在客户端执行以下命令连接:")
	fmt.Printf("  cftunnel relay init --server <服务器IP>:%d --token %s\n", relayServerPort, token)

	return nil
}

func runRelayServerUninstall(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("服务端卸载仅支持 Linux，当前平台: %s", runtime.GOOS)
	}
	exec.Command("systemctl", "disable", "--now", "frps").Run()
	os.Remove("/etc/systemd/system/frps.service")
	os.Remove("/usr/local/bin/frps")
	os.RemoveAll("/etc/frps")
	fmt.Println("✓ frps 服务端已卸载")
	return nil
}

func runRelayServerStatus(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("服务端状态查看仅支持 Linux，当前平台: %s", runtime.GOOS)
	}
	out, err := exec.Command("systemctl", "is-active", "frps").Output()
	if err != nil {
		fmt.Println("frps 服务状态: 未运行")
		return nil
	}
	status := string(out)
	fmt.Printf("frps 服务状态: %s", status)
	return nil
}
