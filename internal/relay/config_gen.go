package relay

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// FrpcConfigPath 返回 frpc.toml 路径
func FrpcConfigPath() string {
	return filepath.Join(config.Dir(), "frpc.toml")
}

// FrpsConfigPath 返回 frps.toml 路径
func FrpsConfigPath() string {
	return filepath.Join(config.Dir(), "frps.toml")
}

// GenerateFrpcConfig 从 config.yml 的 relay 配置生成 frpc.toml
func GenerateFrpcConfig(relay *config.RelayConfig) error {
	if relay.Server == "" {
		return fmt.Errorf("未配置中继服务器，请先执行 cftunnel relay init")
	}

	host, port, err := net.SplitHostPort(relay.Server)
	if err != nil {
		return fmt.Errorf("服务器地址格式错误（应为 IP:端口）: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "serverAddr = %q\n", host)
	fmt.Fprintf(&b, "serverPort = %s\n", port)
	if relay.Token != "" {
		fmt.Fprintf(&b, "auth.token = %q\n", relay.Token)
	}
	b.WriteString("\n")

	for _, rule := range relay.Rules {
		b.WriteString("[[proxies]]\n")
		fmt.Fprintf(&b, "name = %q\n", rule.Name)
		fmt.Fprintf(&b, "type = %q\n", rule.Proto)
		localIP := rule.LocalIP
		if localIP == "" {
			localIP = "127.0.0.1"
		}
		fmt.Fprintf(&b, "localIP = %q\n", localIP)
		fmt.Fprintf(&b, "localPort = %d\n", rule.LocalPort)
		if rule.RemotePort > 0 {
			fmt.Fprintf(&b, "remotePort = %d\n", rule.RemotePort)
		}
		if rule.Domain != "" {
			fmt.Fprintf(&b, "customDomains = [%q]\n", rule.Domain)
		}
		b.WriteString("\n")
	}

	return os.WriteFile(FrpcConfigPath(), []byte(b.String()), 0600)
}

// GenerateFrpsConfig 生成服务端 frps.toml
func GenerateFrpsConfig(bindPort int, token string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "bindPort = %d\n", bindPort)
	if token != "" {
		fmt.Fprintf(&b, "auth.token = %q\n", token)
	}
	return os.WriteFile(FrpsConfigPath(), []byte(b.String()), 0600)
}
