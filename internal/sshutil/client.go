package sshutil

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ConnectConfig SSH 连接参数
type ConnectConfig struct {
	Host     string // IP 或域名
	Port     int    // 默认 22
	User     string // 默认 root
	Password string // 密码认证
	KeyPath  string // 私钥路径
}

// Addr 返回 host:port
func (c *ConnectConfig) Addr() string {
	port := c.Port
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("%s:%d", c.Host, port)
}

// Connect 建立 SSH 连接
// 认证优先级: 指定密钥 > 密码 > SSH Agent > 默认密钥路径
func Connect(cfg *ConnectConfig) (*ssh.Client, error) {
	var authMethods []ssh.AuthMethod

	// 1. 指定密钥文件
	if cfg.KeyPath != "" {
		if m, err := authFromKeyFile(cfg.KeyPath); err == nil {
			authMethods = append(authMethods, m)
		} else {
			return nil, fmt.Errorf("读取密钥 %s 失败: %w", cfg.KeyPath, err)
		}
	}

	// 2. 密码
	if cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(cfg.Password))
	}

	// 3. SSH Agent
	if m, err := authFromAgent(); err == nil {
		authMethods = append(authMethods, m)
	}

	// 4. 默认密钥路径
	if cfg.KeyPath == "" {
		home, _ := os.UserHomeDir()
		for _, name := range []string{"id_ed25519", "id_rsa"} {
			p := filepath.Join(home, ".ssh", name)
			if m, err := authFromKeyFile(p); err == nil {
				authMethods = append(authMethods, m)
			}
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("无可用的 SSH 认证方式，请指定 --key 或 --password")
	}

	// known_hosts 校验
	var hostKeyCallback ssh.HostKeyCallback
	home, _ := os.UserHomeDir()
	knownHostsPath := filepath.Join(home, ".ssh", "known_hosts")
	if cb, err := knownhosts.New(knownHostsPath); err == nil {
		hostKeyCallback = cb
	} else {
		fmt.Println("⚠ 未找到 known_hosts，跳过主机指纹校验")
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	sshConfig := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	client, err := ssh.Dial("tcp", cfg.Addr(), sshConfig)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败 (%s): %w", cfg.Addr(), err)
	}
	return client, nil
}

// authFromKeyFile 从私钥文件创建认证方法
func authFromKeyFile(path string) (ssh.AuthMethod, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, err
	}
	return ssh.PublicKeys(signer), nil
}

// authFromAgent 从 SSH Agent 获取认证
func authFromAgent() (ssh.AuthMethod, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK 未设置")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	agentClient := agent.NewClient(conn)
	return ssh.PublicKeysCallback(agentClient.Signers), nil
}
