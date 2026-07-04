//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
)

type Systemd struct{}

const unitName = "cftunnel"

func (s *Systemd) unitPath() string {
	return "/etc/systemd/system/" + unitName + ".service"
}

func (s *Systemd) Install(binPath, token string) error {
	unit := fmt.Sprintf(`[Unit]
Description=Cloudflare Tunnel (cftunnel)
After=network.target

[Service]
ExecStart=%s tunnel --protocol http2 run --token %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, binPath, token)

	if err := os.WriteFile(s.unitPath(), []byte(unit), 0644); err != nil {
		return err
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return err
	}
	return exec.Command("systemctl", "enable", "--now", unitName).Run()
}

func (s *Systemd) Uninstall() error {
	exec.Command("systemctl", "disable", "--now", unitName).Run()
	return os.Remove(s.unitPath())
}

func (s *Systemd) Running() bool {
	return exec.Command("systemctl", "is-active", "--quiet", unitName).Run() == nil
}

func (s *Systemd) Installed() bool {
	_, err := os.Stat(s.unitPath())
	return err == nil
}

func New() Service {
	return &Systemd{}
}
