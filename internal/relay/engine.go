package relay

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// pidFilePath 返回 frpc PID 文件路径
func pidFilePath() string {
	return filepath.Join(config.Dir(), "frpc.pid")
}

// LogFilePath 返回中继模式日志路径
func LogFilePath() string {
	return filepath.Join(config.LogDir(), "cftunnelX-relay.log")
}

// Start 启动 frpc（后台模式）
func Start() error {
	binPath, err := EnsureFrpc()
	if err != nil {
		return err
	}
	if Running() {
		return fmt.Errorf("frpc 已在运行")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	if err := GenerateFrpcConfig(&cfg.Relay); err != nil {
		return err
	}

	// 日志重定向到文件
	logPath := LogFilePath()
	os.MkdirAll(filepath.Dir(logPath), 0755)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	cmd := exec.Command(binPath, "-c", FrpcConfigPath())
	configureCommand(cmd)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("启动 frpc 失败: %w", err)
	}
	os.WriteFile(pidFilePath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	fmt.Printf("frpc 已启动 (PID: %d)\n", cmd.Process.Pid)
	return nil
}

// Stop 停止 frpc
func Stop() error {
	pid, err := readPID()
	if err != nil {
		return fmt.Errorf("未找到运行中的 frpc")
	}
	if err := processKill(pid); err != nil {
		return fmt.Errorf("停止 frpc 失败: %w", err)
	}
	os.Remove(pidFilePath())
	fmt.Println("frpc 已停止")
	return nil
}

// Running 检查 frpc 是否在运行
func Running() bool {
	pid, err := readPID()
	if err != nil {
		return false
	}
	return processRunning(pid)
}

// PID 返回当前运行的 PID
func PID() int {
	pid, _ := readPID()
	return pid
}

func readPID() (int, error) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0, err
	}
	s := string(data)
	// 去除空白
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return strconv.Atoi(s)
}

// StartQuick 前台运行 frpc（quick --relay 模式，Ctrl+C 退出）
func StartQuick(port, proto string) error {
	binPath, err := EnsureFrpc()
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	if cfg.Relay.Server == "" {
		return fmt.Errorf("未配置中继服务器，请先执行 cftunnel relay init")
	}

	// 创建临时规则
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("端口格式错误: %w", err)
	}
	tmpRelay := config.RelayConfig{
		Server: cfg.Relay.Server,
		Token:  cfg.Relay.Token,
		Rules: []config.RelayRule{{
			Name:       "quick",
			Proto:      proto,
			LocalPort:  portNum,
			RemotePort: portNum,
		}},
	}
	if err := GenerateFrpcConfig(&tmpRelay); err != nil {
		return err
	}

	fmt.Printf("中继穿透: %s://localhost:%s → 远程端口 %s (%s)\n", proto, port, port, cfg.Relay.Server)

	cmd := exec.Command(binPath, "-c", FrpcConfigPath())
	configureCommand(cmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 frpc 失败: %w", err)
	}

	// Ctrl+C 优雅退出
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-sig:
		cmd.Process.Signal(os.Interrupt)
		<-done
	case err := <-done:
		if err != nil {
			return fmt.Errorf("frpc 异常退出: %w", err)
		}
	}
	return nil
}
