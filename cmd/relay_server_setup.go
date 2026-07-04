package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/sshutil"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

var (
	setupHost     string
	setupPort     int
	setupUser     string
	setupKeyPath  string
	setupPassword bool
	setupPassVal  string
	setupFrpsPort int
)

func init() {
	relayServerSetupCmd.Flags().StringVar(&setupHost, "host", "", "服务器 IP 或域名")
	relayServerSetupCmd.Flags().IntVarP(&setupPort, "port", "p", 22, "SSH 端口")
	relayServerSetupCmd.Flags().StringVar(&setupUser, "user", "root", "SSH 用户名")
	relayServerSetupCmd.Flags().StringVar(&setupKeyPath, "key", "", "SSH 私钥路径")
	relayServerSetupCmd.Flags().BoolVar(&setupPassword, "password", false, "使用密码认证（交互输入）")
	relayServerSetupCmd.Flags().StringVar(&setupPassVal, "pass", "", "SSH 密码（非交互模式，供 GUI 调用）")
	relayServerSetupCmd.Flags().IntVar(&setupFrpsPort, "frps-port", 7000, "frps 监听端口")
	relayServerCmd.AddCommand(relayServerSetupCmd)
}

var relayServerSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "通过 SSH 远程安装 frps 服务端",
	Long: `通过 SSH 连接到远程 Linux 服务器，自动安装并配置 frps。
安装完成后自动配置本地客户端连接。

示例:
  cftunnel relay server setup --host 1.2.3.4 --key ~/.ssh/id_rsa
  cftunnel relay server setup --host 1.2.3.4 --password
  cftunnel relay server setup   # 全交互模式`,
	RunE: runRelayServerSetup,
}

func runRelayServerSetup(cmd *cobra.Command, args []string) error {
	// 阶段1: 收集 SSH 连接参数
	sshCfg, err := collectSSHConfig()
	if err != nil {
		return err
	}

	// 阶段2: 建立 SSH 连接
	fmt.Printf("正在连接 %s@%s ...\n", sshCfg.User, sshCfg.Addr())
	client, err := sshutil.Connect(sshCfg)
	if err != nil {
		return err
	}
	defer client.Close()
	fmt.Println("✓ SSH 连接成功")

	// 阶段3: 远程环境预检
	if err := preflightChecks(client); err != nil {
		return err
	}

	// 阶段4: 远程安装 frps
	fmt.Println("\n正在安装 frps ...")
	script := buildInstallScript(setupFrpsPort)
	if err := sshutil.RunScript(client, script); err != nil {
		return fmt.Errorf("远程安装失败: %w", err)
	}

	// 阶段5: 读回 token，配置本地
	token, err := sshutil.RunCommandOutput(client,
		`grep 'auth.token' /etc/frps/frps.toml | sed 's/.*"\(.*\)"/\1/'`)
	if err != nil || token == "" {
		fmt.Println("⚠ 无法自动读取 token，请手动查看远程 /etc/frps/frps.toml 并执行:")
		fmt.Printf("  cftunnel relay init --server %s:%d --token <token>\n", sshCfg.Host, setupFrpsPort)
		return nil
	}

	serverAddr := fmt.Sprintf("%s:%d", sshCfg.Host, setupFrpsPort)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.Relay.Server = serverAddr
	cfg.Relay.Token = token
	if err := cfg.Save(); err != nil {
		return err
	}

	// 阶段6: 输出结果
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════╗")
	fmt.Println("║       ✅ frps 远程安装完成!                ║")
	fmt.Println("╚════════════════════════════════════════════╝")
	fmt.Printf("  服务器: %s\n", serverAddr)
	fmt.Printf("  Token:  %s\n", token)
	fmt.Println()
	fmt.Println("本地已自动配置，可直接使用:")
	fmt.Println("  cftunnel relay add <名称> --proto tcp --local <端口>")
	fmt.Println("  cftunnel relay up")
	return nil
}

func collectSSHConfig() (*sshutil.ConnectConfig, error) {
	host := setupHost
	user := setupUser
	port := setupPort
	keyPath := setupKeyPath
	var password string

	// 全交互模式：未传 --host 时启动 huh 表单
	if host == "" {
		var portStr string
		var authChoice string

		err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().Title("服务器地址 (IP 或域名)").Value(&host),
				huh.NewInput().Title("SSH 端口").Value(&portStr).Placeholder("22"),
				huh.NewInput().Title("用户名").Value(&user).Placeholder("root"),
				huh.NewSelect[string]().
					Title("认证方式").
					Options(
						huh.NewOption("SSH 密钥 (自动检测)", "key"),
						huh.NewOption("指定密钥路径", "key_path"),
						huh.NewOption("密码", "password"),
					).
					Value(&authChoice),
			),
		).Run()
		if err != nil {
			return nil, err
		}

		if portStr != "" {
			fmt.Sscanf(portStr, "%d", &port)
		}
		if user == "" {
			user = "root"
		}

		switch authChoice {
		case "key_path":
			err := huh.NewForm(huh.NewGroup(
				huh.NewInput().Title("私钥路径").Value(&keyPath).
					Placeholder("~/.ssh/id_rsa"),
			)).Run()
			if err != nil {
				return nil, err
			}
		case "password":
			setupPassword = true
		}
	}

	// 非交互密码（--pass 直接传值，供 GUI 调用）
	if setupPassVal != "" {
		password = setupPassVal
	} else if setupPassword {
		// 交互式密码输入
		err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("SSH 密码").Value(&password).
				EchoMode(huh.EchoModePassword),
		)).Run()
		if err != nil {
			return nil, err
		}
	}

	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("服务器地址不能为空")
	}

	return &sshutil.ConnectConfig{
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		KeyPath:  keyPath,
	}, nil
}

func preflightChecks(client *ssh.Client) error {
	// 检测 Linux
	osName, err := sshutil.RunCommandOutput(client, "uname -s")
	if err != nil || osName != "Linux" {
		return fmt.Errorf("远程服务器不是 Linux (检测到: %s)", osName)
	}

	// 检测 root
	uid, err := sshutil.RunCommandOutput(client, "id -u")
	if err != nil || uid != "0" {
		return fmt.Errorf("需要 root 权限 (当前 uid: %s)", uid)
	}

	// 检测架构
	arch, err := sshutil.RunCommandOutput(client, "uname -m")
	if err != nil {
		return fmt.Errorf("无法检测架构: %w", err)
	}
	switch arch {
	case "x86_64", "aarch64", "armv7l":
		fmt.Printf("✓ 远程环境: Linux %s, root\n", arch)
	default:
		return fmt.Errorf("不支持的架构: %s", arch)
	}

	// 检测是否已安装
	existing, _ := sshutil.RunCommandOutput(client, "systemctl is-active frps 2>/dev/null")
	if existing == "active" {
		return fmt.Errorf("frps 已在运行，如需重装请先在服务器上卸载: systemctl disable --now frps")
	}

	// 检测 curl
	_, err = sshutil.RunCommandOutput(client, "which curl")
	if err != nil {
		return fmt.Errorf("远程服务器缺少 curl，请先安装: apt install curl 或 yum install curl")
	}

	return nil
}

func buildInstallScript(bindPort int) string {
	return fmt.Sprintf(`set -euo pipefail
FRP_VERSION="0.66.0"
BIND_PORT=%d
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/frps"

ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  FRP_ARCH="amd64" ;;
    aarch64) FRP_ARCH="arm64" ;;
    armv7l)  FRP_ARCH="arm" ;;
    *)       echo "[ERROR] 不支持的架构: $ARCH"; exit 1 ;;
esac

FILENAME="frp_${FRP_VERSION}_linux_${FRP_ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/${FILENAME}"
MIRRORS=("https://ghfast.top/" "https://gh-proxy.com/" "https://ghproxy.cn/" "")

TMP_DIR=$(mktemp -d)
trap "rm -rf $TMP_DIR" EXIT

download_ok=false
for mirror in "${MIRRORS[@]}"; do
    url="${mirror}${DOWNLOAD_URL}"
    echo "[INFO] 尝试下载: ${mirror:-GitHub} ..."
    if curl -fsSL --connect-timeout 10 -o "$TMP_DIR/$FILENAME" "$url"; then
        download_ok=true; echo "[INFO] 下载成功"; break
    fi
    echo "[WARN] 下载失败，尝试下一个源..."
done
[ "$download_ok" = false ] && echo "[ERROR] 所有下载源均失败" && exit 1

echo "[INFO] 正在解压..."
tar -xzf "$TMP_DIR/$FILENAME" -C "$TMP_DIR"
install -m 755 "$TMP_DIR/frp_${FRP_VERSION}_linux_${FRP_ARCH}/frps" "$INSTALL_DIR/frps"
echo "[INFO] frps 已安装到 $INSTALL_DIR/frps"

TOKEN=$(head -c 16 /dev/urandom | xxd -p)
mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/frps.toml" <<TOML
bindPort = ${BIND_PORT}
auth.token = "${TOKEN}"
TOML
chmod 600 "$CONFIG_DIR/frps.toml"

cat > /etc/systemd/system/frps.service <<UNIT
[Unit]
Description=frps relay server (cftunnel)
After=network.target
[Service]
ExecStart=${INSTALL_DIR}/frps -c ${CONFIG_DIR}/frps.toml
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now frps
echo "[INFO] frps 服务已启动 (端口 ${BIND_PORT})"
`, bindPort)
}
