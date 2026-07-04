package sshutil

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// RunCommand 执行单条命令，stdout/stderr 实时回显
func RunCommand(client *ssh.Client, command string) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("创建 SSH session 失败: %w", err)
	}
	defer session.Close()

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	return session.Run(command)
}

// RunCommandOutput 执行命令并捕获输出（不回显）
func RunCommandOutput(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("创建 SSH session 失败: %w", err)
	}
	defer session.Close()

	out, err := session.CombinedOutput(command)
	return strings.TrimSpace(string(out)), err
}

// RunScript 通过 stdin 传入多行脚本执行，实时回显
func RunScript(client *ssh.Client, script string) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("创建 SSH session 失败: %w", err)
	}
	defer session.Close()

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}

	if err := session.Shell(); err != nil {
		return err
	}

	io.WriteString(stdin, script+"\nexit $?\n")
	stdin.Close()

	return session.Wait()
}
