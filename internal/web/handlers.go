package web

import (
	"bufio"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shiranzby/cftunnelX/internal/authproxy"
	"github.com/shiranzby/cftunnelX/internal/cfapi"
	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/shiranzby/cftunnelX/internal/daemon"
	"github.com/shiranzby/cftunnelX/internal/logutil"
	"github.com/shiranzby/cftunnelX/internal/relay"
	"github.com/shiranzby/cftunnelX/internal/service"
	"github.com/shiranzby/cftunnelX/internal/sshutil"
	"golang.org/x/crypto/ssh"
)

// netDialTimeout 包装 net.DialTimeout
func netDialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, address, timeout)
}

// netLookupHost 包装 net.LookupHost
func netLookupHost(host string) ([]string, error) {
	return net.LookupHost(host)
}

// extractPortFromService 从 service 字符串提取端口
func extractPortFromService(service string) string {
	s := service
	for _, prefix := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, prefix)
	}
	_, port, err := net.SplitHostPort(s)
	if err != nil {
		return ""
	}
	return port
}

// ========== 配置管理 ==========

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]interface{}{
			"auth": map[string]string{
				"api_token":  cfg.Auth.APIToken,
				"account_id": cfg.Auth.AccountID,
			},
			"tunnel": cfg.Tunnel,
			"routes": cfg.Routes,
			"relay":  cfg.Relay,
			"web_ui": map[string]interface{}{
				"port":           cfg.WebUI.Port,
				"username":       cfg.WebUI.Username,
				"theme":          cfg.WebUI.Theme,
				"remote_enabled": cfg.WebUI.RemoteEnabled,
				"remote_domain":  cfg.WebUI.RemoteDomain,
			},
		})

	case http.MethodPost:
		var body struct {
			Auth struct {
				APIToken  string `json:"api_token"`
				AccountID string `json:"account_id"`
			} `json:"auth"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "请求格式错误")
			return
		}
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if body.Auth.APIToken != "" {
			cfg.Auth.APIToken = strings.TrimSpace(body.Auth.APIToken)
		}
		if body.Auth.AccountID != "" {
			cfg.Auth.AccountID = strings.TrimSpace(body.Auth.AccountID)
		}
		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.cfg = cfg
		writeOK(w, map[string]string{"status": "ok"})

	default:
		writeError(w, 405, "method not allowed")
	}
}

// ========== API 测试（仅测 Cloudflare API） ==========

func (s *Server) handleConfigTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if cfg.Auth.APIToken == "" {
		writeError(w, 400, "API Token 未配置")
		return
	}

	client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	zones, err := client.ListZones(ctx)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		writeOK(w, map[string]interface{}{
			"ok":      false,
			"error":   err.Error(),
			"latency": latency,
		})
		return
	}

	zoneNames := make([]string, 0, len(zones))
	for _, z := range zones {
		zoneNames = append(zoneNames, z.Name)
	}

	writeOK(w, map[string]interface{}{
		"ok":         true,
		"latency":    latency,
		"zones":      zoneNames,
		"zone_count": len(zones),
	})
}

// ========== cloudflared 诊断（独立检测，只读不下载） ==========

func (s *Server) handleConfigDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	// 只检测已安装的 cloudflared，不触发下载
	cf := checkCloudflaredReadOnly()
	writeOK(w, cf)
}

// checkCloudflaredReadOnly 只读检测 cloudflared（不下载）
func checkCloudflaredReadOnly() map[string]interface{} {
	result := map[string]interface{}{
		"installed": false,
		"running":   false,
	}
	// 检查配置中的路径
	cfg, err := config.Load()
	if err == nil && cfg.Cloudflared.Path != "" {
		if _, err := os.Stat(cfg.Cloudflared.Path); err == nil {
			result["installed"] = true
			result["path"] = cfg.Cloudflared.Path
			// 获取版本
			if out, err := hiddenCommand(cfg.Cloudflared.Path, "version").CombinedOutput(); err == nil {
				result["version"] = strings.TrimSpace(string(out))
			}
		}
	}
	// 检查默认下载位置
	binPath := daemon.CloudflaredPath()
	if binPath != "" {
		if _, err := os.Stat(binPath); err == nil {
			result["installed"] = true
			result["path"] = binPath
			if out, err := hiddenCommand(binPath, "version").CombinedOutput(); err == nil {
				result["version"] = strings.TrimSpace(string(out))
			}
		}
	}
	// 检查便携版内置位置
	if p, ok := daemon.BundledCloudflaredPath(); ok {
		result["installed"] = true
		result["path"] = p
		result["bundled"] = true
		if out, err := hiddenCommand(p, "version").CombinedOutput(); err == nil {
			result["version"] = strings.TrimSpace(string(out))
		}
	}
	// 检查系统 PATH
	if p, err := exec.LookPath("cloudflared"); err == nil {
		result["installed"] = true
		result["path"] = p
		if out, err := hiddenCommand(p, "version").CombinedOutput(); err == nil {
			result["version"] = strings.TrimSpace(string(out))
		}
	}
	// 运行状态
	if daemon.Running() {
		result["running"] = true
		result["pid"] = daemon.PID()
	}
	return result
}

// ========== 状态查询 ==========

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	result := map[string]interface{}{
		"cloud": nil,
		"relay": nil,
		"quick": nil,
	}

	// 多隧道状态
	runningTunnels := daemon.RunningTunnels()
	tunnels := make([]map[string]interface{}, 0, len(cfg.Tunnels))
	for _, t := range cfg.Tunnels {
		pid := 0
		running := false
		if p, ok := runningTunnels[t.ID]; ok {
			pid = p
			running = true
		}
		routes := make([]map[string]interface{}, 0, len(t.Routes))
		for _, r := range t.Routes {
			routes = append(routes, map[string]interface{}{
				"name": r.Name, "hostname": r.Hostname, "service": r.Service, "auth": r.Auth != nil,
			})
		}
		tunnels = append(tunnels, map[string]interface{}{
			"tunnel_id":   t.ID,
			"tunnel_name": t.Name,
			"running":     running,
			"pid":         pid,
			"routes":      routes,
		})
	}
	if len(tunnels) > 0 {
		// 统计运行中的隧道数和总路由数
		runningCount := 0
		totalRoutes := 0
		for _, t := range tunnels {
			if t["running"].(bool) {
				runningCount++
			}
			if rs, ok := t["routes"].([]map[string]interface{}); ok {
				totalRoutes += len(rs)
			}
		}
		result["cloud"] = map[string]interface{}{
			"tunnels":       tunnels,
			"tunnel_count":  len(tunnels),
			"running_count": runningCount,
			"total_routes":  totalRoutes,
		}
	} else if cfg.Tunnel.ID != "" {
		// 向后兼容单隧道
		running := daemon.Running()
		pid := 0
		if running {
			pid = daemon.PID()
		}
		routes := make([]map[string]interface{}, 0, len(cfg.Routes))
		for _, r := range cfg.Routes {
			routes = append(routes, map[string]interface{}{
				"name": r.Name, "hostname": r.Hostname, "service": r.Service, "auth": r.Auth != nil,
			})
		}
		result["cloud"] = map[string]interface{}{
			"tunnels": []map[string]interface{}{{
				"tunnel_id": cfg.Tunnel.ID, "tunnel_name": cfg.Tunnel.Name,
				"running": running, "pid": pid, "routes": routes,
			}},
		}
	}

	if cfg.Relay.Server != "" {
		running := relay.Running()
		pid := 0
		if running {
			pid = relay.PID()
		}
		rules := make([]map[string]interface{}, 0, len(cfg.Relay.Rules))
		for _, r := range cfg.Relay.Rules {
			rules = append(rules, map[string]interface{}{
				"name":        r.Name,
				"proto":       r.Proto,
				"local_port":  r.LocalPort,
				"remote_port": r.RemotePort,
				"domain":      r.Domain,
			})
		}
		result["relay"] = map[string]interface{}{
			"server":  cfg.Relay.Server,
			"running": running,
			"pid":     pid,
			"rules":   rules,
		}
	}

	// 免域名模式状态
	quickRunning := daemon.QuickRunning()
	result["quick"] = map[string]interface{}{
		"running": quickRunning,
		"url":     daemon.QuickURL(),
		"pid":     daemon.QuickPID(),
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	result["system"] = map[string]interface{}{
		"mode":          modeName(),
		"started_at":    s.started.Format("2006-01-02 15:04:05"),
		"uptime_sec":    int64(time.Since(s.started).Seconds()),
		"local_ips":     localIPs(),
		"web_running":   true,
		"web_port":      s.listenPort(),
		"cloud_running": len(runningTunnels) > 0 || daemon.Running(),
		"relay_running": relay.Running(),
		"cpu":           runtime.NumCPU(),
		"goroutines":    runtime.NumGoroutine(),
		"memory_alloc":  ms.Alloc,
		"memory_sys":    ms.Sys,
		"kernel_memory": ms.Sys,
		"log_count":     logLineCount(logFilePath()),
		"traffic": map[string]interface{}{
			"active_connections": runningCount(runningTunnels) + boolInt(relay.Running()),
			"upload_speed":       int64(0),
			"download_speed":     int64(0),
			"upload_total":       int64(0),
			"download_total":     int64(0),
			"cpu_usage":          runtime.NumGoroutine(),
			"memory_usage":       ms.Alloc,
			"kernel_memory":      ms.Sys,
		},
	}

	writeOK(w, result)
}

func runningCount(items map[string]int) int {
	n := 0
	for _, pid := range items {
		if pid > 0 {
			n++
		}
	}
	return n
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// ========== 隧道管理（多隧道） ==========

func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "请求格式错误")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			writeError(w, 400, "隧道名称不能为空")
			return
		}

		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if cfg.Auth.APIToken == "" {
			writeError(w, 400, "请先配置 API Token 和 Account ID")
			return
		}

		client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
		ctx := context.Background()

		tunnel, err := client.CreateTunnel(ctx, name)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		token, err := client.GetTunnelToken(ctx, tunnel.ID)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}

		newTunnel := config.TunnelConfig{ID: tunnel.ID, Name: tunnel.Name, Token: token}
		cfg.Tunnels = append(cfg.Tunnels, newTunnel)
		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.cfg = cfg
		logLine("隧道创建成功: %s (ID: %s)", tunnel.Name, tunnel.ID)
		writeOK(w, map[string]interface{}{
			"id":   tunnel.ID,
			"name": tunnel.Name,
		})

	case http.MethodDelete:
		// 删除指定隧道（通过 query 参数 id）
		tunnelID := r.URL.Query().Get("id")
		if tunnelID == "" {
			writeError(w, 400, "缺少隧道 ID")
			return
		}
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		tunnel := cfg.FindTunnel(tunnelID)
		if tunnel == nil {
			writeError(w, 404, "隧道不存在")
			return
		}

		// 停止本地隧道进程，随后尽力同步删除远端资源。
		if daemon.RunningTunnel(tunnelID) {
			if err := daemon.StopTunnel(tunnelID); err != nil {
				writeError(w, 500, err.Error())
				return
			}
		}
		time.Sleep(800 * time.Millisecond)

		client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
		ctx := context.Background()
		warnings := make([]string, 0)

		// 清理 DNS
		for _, r := range tunnel.Routes {
			if r.DNSRecordID != "" && r.ZoneID != "" {
				if err := client.DeleteDNSRecord(ctx, r.ZoneID, r.DNSRecordID); err != nil {
					if cfapi.IsDNSRecordNotFound(err) {
						warnings = append(warnings, fmt.Sprintf("DNS 记录已不存在: %s", r.Hostname))
					} else {
						writeError(w, 500, err.Error())
						return
					}
				}
			}
		}
		if err := client.DeleteTunnel(ctx, tunnelID); err != nil {
			switch {
			case cfapi.IsTunnelNotFound(err):
				warnings = append(warnings, "远端隧道已不存在，已清理本地记录")
			case cfapi.IsTunnelActiveConnections(err):
				warnings = append(warnings, "远端仍提示 active connections，本地记录已同步删除；Cloudflare 端会在连接释放后允许再次清理")
			default:
				writeError(w, 500, err.Error())
				return
			}
		}

		cfg.RemoveTunnel(tunnelID)
		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.cfg = cfg
		logLine("隧道销毁: %s", tunnelID)
		writeOK(w, map[string]interface{}{"status": "destroyed", "warnings": warnings})

	default:
		writeError(w, 405, "method not allowed")
	}
}

func (s *Server) handleTunnelOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	byID := make(map[string]config.TunnelConfig, len(cfg.Tunnels))
	for _, t := range cfg.Tunnels {
		byID[t.ID] = t
	}
	used := make(map[string]bool, len(cfg.Tunnels))
	ordered := make([]config.TunnelConfig, 0, len(cfg.Tunnels))
	for _, id := range body.IDs {
		id = strings.TrimSpace(id)
		if t, ok := byID[id]; ok && !used[id] {
			ordered = append(ordered, t)
			used[id] = true
		}
	}
	for _, t := range cfg.Tunnels {
		if !used[t.ID] {
			ordered = append(ordered, t)
		}
	}
	cfg.Tunnels = ordered
	if err := cfg.Save(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.cfg = cfg
	writeOK(w, map[string]string{"status": "ordered"})
}

func (s *Server) handleTunnelUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	tunnelID := r.URL.Query().Get("id")
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	if tunnelID != "" {
		// 多隧道模式
		tunnel := cfg.FindTunnel(tunnelID)
		if tunnel == nil {
			writeError(w, 404, "隧道不存在")
			return
		}
		if daemon.RunningTunnel(tunnelID) {
			writeError(w, 400, "隧道已在运行")
			return
		}
		// 同步 ingress
		if len(tunnel.Routes) > 0 {
			client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
			var rules []cfapi.IngressRule
			for _, r := range tunnel.Routes {
				rules = append(rules, cfapi.IngressRule{Hostname: r.Hostname, Service: r.Service})
			}
			if err := client.PushIngressConfig(context.Background(), tunnelID, rules); err != nil {
				writeError(w, 500, err.Error())
				return
			}
		}
		logLine("启动隧道: %s (%s)", tunnel.Name, tunnelID)
		if err := daemon.StartTunnel(tunnelID, tunnel.Token); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	} else {
		// 向后兼容单隧道
		if cfg.Tunnel.Token == "" {
			writeError(w, 400, "未配置隧道")
			return
		}
		if daemon.Running() {
			writeError(w, 400, "隧道已在运行")
			return
		}
		if err := daemon.Start(cfg.Tunnel.Token); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}

	writeOK(w, map[string]string{"status": "starting"})
}

func (s *Server) handleTunnelDown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	tunnelID := r.URL.Query().Get("id")
	if tunnelID != "" {
		logLine("停止隧道: %s", tunnelID)
		if err := daemon.StopTunnel(tunnelID); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	} else {
		if err := daemon.Stop(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	writeOK(w, map[string]string{"status": "stopped"})
}

// ========== 路由管理 ==========

func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		// 返回所有隧道的所有路由（多隧道模式）
		writeOK(w, cfg.AllRoutes())

	case http.MethodPut:
		// 路由更新（前端 inline 编辑后触发：先删后建）
		var body struct {
			Name     string `json:"name"`
			NewName  string `json:"new_name"`
			Port     string `json:"port"`
			Domain   string `json:"domain"`
			TunnelID string `json:"tunnel_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "请求格式错误")
			return
		}
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		route := cfg.FindRoute(body.Name)
		if route == nil {
			writeError(w, 404, "路由不存在")
			return
		}
		logLine("更新路由 %s → name=%s port=%s domain=%s", body.Name, body.NewName, body.Port, body.Domain)
		client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
		ctx := context.Background()
		if body.Domain != "" && body.Domain != route.Hostname && body.TunnelID != "" {
			zone, err := findZoneForDomain(client, ctx, body.Domain)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			target := body.TunnelID + ".cfargotunnel.com"
			if route.DNSRecordID != "" && route.ZoneID == zone.ID {
				if err := client.UpdateCNAME(ctx, zone.ID, route.DNSRecordID, body.Domain, target); err != nil {
					writeError(w, 500, err.Error())
					return
				}
			} else {
				if route.DNSRecordID != "" && route.ZoneID != "" {
					if err := client.DeleteDNSRecord(ctx, route.ZoneID, route.DNSRecordID); err != nil {
						writeError(w, 500, err.Error())
						return
					}
				}
				recordID, err := client.CreateCNAME(ctx, zone.ID, body.Domain, target)
				if err != nil {
					writeError(w, 500, err.Error())
					return
				}
				route.DNSRecordID = recordID
			}
			route.ZoneID = zone.ID
		}
		// 更新字段
		if body.NewName != "" {
			route.Name = body.NewName
		}
		if body.Port != "" {
			route.Service = "http://localhost:" + body.Port
		}
		if body.Domain != "" {
			route.Hostname = body.Domain
		}
		// 推送 ingress
		if body.TunnelID != "" {
			tunnel := cfg.FindTunnel(body.TunnelID)
			if tunnel != nil {
				var rules []cfapi.IngressRule
				for _, r := range tunnel.Routes {
					rules = append(rules, cfapi.IngressRule{Hostname: r.Hostname, Service: r.Service})
				}
				if err := client.PushIngressConfig(ctx, body.TunnelID, rules); err != nil {
					writeError(w, 500, err.Error())
					return
				}
			}
		}
		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.cfg = cfg
		writeOK(w, map[string]string{"status": "updated"})

	case http.MethodPost:
		var body struct {
			Name     string `json:"name"`
			Port     string `json:"port"`
			Domain   string `json:"domain"`
			AuthUser string `json:"auth_user"`
			AuthPass string `json:"auth_pass"`
			TunnelID string `json:"tunnel_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "请求格式错误")
			return
		}

		name := strings.TrimSpace(body.Name)
		port := strings.TrimSpace(body.Port)
		domain := strings.TrimSpace(body.Domain)

		if name == "" || port == "" || domain == "" {
			writeError(w, 400, "服务名称、本地端口、域名均不能为空")
			return
		}

		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}

		// 查找目标隧道
		var tunnel *config.TunnelConfig
		if body.TunnelID != "" {
			tunnel = cfg.FindTunnel(body.TunnelID)
			if tunnel == nil {
				writeError(w, 400, "隧道不存在")
				return
			}
		} else if len(cfg.Tunnels) > 0 {
			tunnel = &cfg.Tunnels[0] // 默认第一个
		} else if cfg.Tunnel.ID != "" {
			// 向后兼容
			cfg.Tunnels = []config.TunnelConfig{cfg.Tunnel}
			cfg.Tunnels[0].Routes = cfg.Routes
			cfg.Tunnel = config.TunnelConfig{}
			cfg.Routes = nil
			tunnel = &cfg.Tunnels[0]
		} else {
			writeError(w, 400, "请先创建隧道")
			return
		}

		if cfg.FindRoute(name) != nil {
			writeError(w, 400, fmt.Sprintf("路由 %s 已存在", name))
			return
		}

		client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
		ctx := context.Background()

		zone, err := findZoneForDomain(client, ctx, domain)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}

		target := tunnel.ID + ".cfargotunnel.com"
		existingRecord, err := client.FindDNSRecord(ctx, zone.ID, domain)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		var recordID string
		if existingRecord != "" {
			if err := client.UpdateCNAME(ctx, zone.ID, existingRecord, domain, target); err != nil {
				writeError(w, 500, err.Error())
				return
			}
			recordID = existingRecord
		} else {
			recordID, err = client.CreateCNAME(ctx, zone.ID, domain, target)
			if err != nil {
				writeError(w, 500, err.Error())
				return
			}
		}

		svc := "http://localhost:" + port
		route := config.RouteConfig{
			Name:        name,
			Hostname:    domain,
			Service:     svc,
			ZoneID:      zone.ID,
			DNSRecordID: recordID,
		}

		if body.AuthUser != "" && body.AuthPass != "" {
			route.Auth = &config.AuthProxy{
				Username:   body.AuthUser,
				Password:   body.AuthPass,
				SigningKey: hex.EncodeToString(authproxy.RandomKey()),
			}
		}

		tunnel.Routes = append(tunnel.Routes, route)
		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}

		var rules []cfapi.IngressRule
		for _, r := range tunnel.Routes {
			rules = append(rules, cfapi.IngressRule{Hostname: r.Hostname, Service: r.Service})
		}
		if err := client.PushIngressConfig(ctx, tunnel.ID, rules); err != nil {
			writeError(w, 500, err.Error())
			return
		}

		s.cfg = cfg
		logLine("路由添加: %s → %s (隧道: %s)", route.Name, route.Hostname, body.TunnelID)
		writeOK(w, route)

	case http.MethodDelete:
		name := strings.TrimPrefix(r.URL.Path, "/api/routes/")
		if name == "" {
			writeError(w, 400, "缺少路由名称")
			return
		}

		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}

		// 找到并删除路由（多隧道优先）
		tunnelID, remainingRoutes, found, err := deleteRouteFromConfig(cfg, name)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}

		if !found {
			writeError(w, 404, "路由不存在")
			return
		}

		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}

		// 推送 ingress（使用正确的隧道 ID）
		if tunnelID != "" {
			var rules []cfapi.IngressRule
			for _, r := range remainingRoutes {
				rules = append(rules, cfapi.IngressRule{Hostname: r.Hostname, Service: r.Service})
			}
			cfg2, _ := config.Load()
			client2 := cfapi.New(cfg2.Auth.APIToken, cfg2.Auth.AccountID)
			if err := client2.PushIngressConfig(context.Background(), tunnelID, rules); err != nil {
				writeError(w, 500, err.Error())
				return
			}
		}

		s.cfg = cfg
		writeOK(w, map[string]string{"status": "deleted"})

	default:
		writeError(w, 405, "method not allowed")
	}
}

func findZoneForDomain(client *cfapi.Client, ctx context.Context, domain string) (*cfapi.ZoneInfo, error) {
	zoneList, err := client.ListZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取域名列表失败: %w", err)
	}
	for _, z := range zoneList {
		if domain == z.Name || strings.HasSuffix(domain, "."+z.Name) {
			return &cfapi.ZoneInfo{ID: z.ID, Name: z.Name}, nil
		}
	}
	return nil, fmt.Errorf("未找到域名 %s 对应的 Zone，请确认域名已添加到 Cloudflare", domain)
}

// ========== 免域名模式 ==========

func (s *Server) handleQuick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	writeOK(w, map[string]interface{}{
		"running": daemon.QuickRunning(),
		"url":     daemon.QuickURL(),
		"pid":     daemon.QuickPID(),
	})
}

func (s *Server) handleQuickUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Port string `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	port := strings.TrimSpace(body.Port)
	if port == "" {
		writeError(w, 400, "本地端口不能为空")
		return
	}

	urlCh, err := daemon.StartQuickBackground(port)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	logLine("免域名模式启动: 端口 %s", port)

	// 等待 URL（最多 30 秒，由内部超时控制）
	select {
	case url := <-urlCh:
		if url == "" {
			writeError(w, 500, "30 秒内未获取到临时域名，请查看日志")
			return
		}
		logLine("免域名URL获取成功: %s", url)
		writeOK(w, map[string]interface{}{
			"status": "running",
			"url":    url,
			"pid":    daemon.QuickPID(),
		})
	case <-time.After(35 * time.Second):
		writeError(w, 500, "启动超时")
	}
}

func (s *Server) handleQuickDown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	logLine("免域名模式停止")
	if err := daemon.StopQuick(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeOK(w, map[string]string{"status": "stopped"})
}

// ========== Relay 配置 ==========

func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, cfg.Relay)
		return
	}

	if r.Method == http.MethodPost {
		var body struct {
			Server string `json:"server"`
			Token  string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "请求格式错误")
			return
		}

		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		cfg.Relay.Server = strings.TrimSpace(body.Server)
		cfg.Relay.Token = strings.TrimSpace(body.Token)
		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.cfg = cfg
		writeOK(w, map[string]string{"status": "ok"})
		return
	}
	writeError(w, 405, "method not allowed")
}

func (s *Server) handleRelayRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, cfg.Relay.Rules)

	case http.MethodPost:
		var body struct {
			Name       string `json:"name"`
			Proto      string `json:"proto"`
			LocalPort  int    `json:"local_port"`
			RemotePort int    `json:"remote_port"`
			Domain     string `json:"domain"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "请求格式错误")
			return
		}
		if body.Name == "" || body.LocalPort == 0 {
			writeError(w, 400, "名称和本地端口不能为空")
			return
		}
		if body.Proto == "" {
			body.Proto = "tcp"
		}

		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if cfg.FindRelayRule(body.Name) != nil {
			writeError(w, 400, "规则已存在")
			return
		}

		rule := config.RelayRule{
			Name:       body.Name,
			Proto:      body.Proto,
			LocalPort:  body.LocalPort,
			RemotePort: body.RemotePort,
			Domain:     body.Domain,
		}
		cfg.Relay.Rules = append(cfg.Relay.Rules, rule)
		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.cfg = cfg
		writeOK(w, rule)

	case http.MethodDelete:
		name := strings.TrimPrefix(r.URL.Path, "/api/relay/rules/")
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if !cfg.RemoveRelayRule(name) {
			writeError(w, 404, "规则不存在")
			return
		}
		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.cfg = cfg
		writeOK(w, map[string]string{"status": "deleted"})

	default:
		writeError(w, 405, "method not allowed")
	}
}

func (s *Server) handleRelayRulesOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	byName := make(map[string]config.RelayRule, len(cfg.Relay.Rules))
	for _, rule := range cfg.Relay.Rules {
		byName[rule.Name] = rule
	}
	used := make(map[string]bool, len(cfg.Relay.Rules))
	ordered := make([]config.RelayRule, 0, len(cfg.Relay.Rules))
	for _, name := range body.Names {
		name = strings.TrimSpace(name)
		if rule, ok := byName[name]; ok && !used[name] {
			ordered = append(ordered, rule)
			used[name] = true
		}
	}
	for _, rule := range cfg.Relay.Rules {
		if !used[rule.Name] {
			ordered = append(ordered, rule)
		}
	}
	cfg.Relay.Rules = ordered
	if err := cfg.Save(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.cfg = cfg
	writeOK(w, map[string]string{"status": "ordered"})
}

func (s *Server) handleRelayUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if cfg.Relay.Server == "" {
		writeError(w, 400, "未配置中继服务器")
		return
	}
	if relay.Running() {
		writeError(w, 400, "中继已在运行")
		return
	}
	if err := relay.Start(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeOK(w, map[string]string{"status": "starting"})
}

func (s *Server) handleRelayDown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if err := relay.Stop(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeOK(w, map[string]string{"status": "stopped"})
}

// ========== Relay 链路检测 ==========

func (s *Server) handleRelayCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if cfg.Relay.Server == "" {
		writeError(w, 400, "未配置中继服务器")
		return
	}
	result := relay.Check(&cfg.Relay, "")
	writeOK(w, result)
}

// ========== Relay 系统服务（install/uninstall） ==========

func (s *Server) handleRelayService(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// 检查服务状态
		status := checkRelayServiceStatus()
		writeOK(w, status)

	case http.MethodPost:
		// 注册中继客户端为系统服务
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if cfg.Relay.Server == "" {
			writeError(w, 400, "未配置中继服务器")
			return
		}
		if len(cfg.Relay.Rules) == 0 {
			writeError(w, 400, "暂无中继规则")
			return
		}
		binPath, err := relay.EnsureFrpc()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if err := relay.GenerateFrpcConfig(&cfg.Relay); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if err := installRelayService(binPath); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]string{"status": "installed"})

	case http.MethodDelete:
		if err := uninstallRelayService(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]string{"status": "uninstalled"})

	default:
		writeError(w, 405, "method not allowed")
	}
}

// checkRelayServiceStatus 检查中继客户端系统服务状态
func checkRelayServiceStatus() map[string]interface{} {
	switch runtime.GOOS {
	case "windows":
		out, err := hiddenCommand("sc", "query", "cftunnelX-relay").Output()
		if err != nil {
			return map[string]interface{}{"installed": false, "running": false}
		}
		running := strings.Contains(string(out), "RUNNING")
		return map[string]interface{}{"installed": true, "running": running}
	case "linux":
		out, err := exec.Command("systemctl", "is-active", "cftunnelX-relay").Output()
		if err != nil {
			return map[string]interface{}{"installed": false, "running": false}
		}
		running := strings.TrimSpace(string(out)) == "active"
		return map[string]interface{}{"installed": true, "running": running}
	case "darwin":
		out, err := exec.Command("launchctl", "list", "com.cftunnelX.frpc").Output()
		if err != nil {
			return map[string]interface{}{"installed": false, "running": false}
		}
		return map[string]interface{}{"installed": true, "running": len(out) > 0}
	}
	return map[string]interface{}{"installed": false, "running": false}
}

func installRelayService(binPath string) error {
	switch runtime.GOOS {
	case "windows":
		binArg := fmt.Sprintf(`%s -c %s`, binPath, relay.FrpcConfigPath())
		if err := hiddenCommand("sc", "create", "cftunnelX-relay", "binPath=", binArg, "start=", "auto").Run(); err != nil {
			return fmt.Errorf("创建服务失败: %w", err)
		}
		return hiddenCommand("sc", "start", "cftunnelX-relay").Run()
	case "linux":
		unit := fmt.Sprintf(`[Unit]
Description=cftunnelX relay (frpc)
After=network.target

[Service]
ExecStart=%s -c %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, binPath, relay.FrpcConfigPath())
		if err := os.WriteFile("/etc/systemd/system/cftunnelX-relay.service", []byte(unit), 0644); err != nil {
			return err
		}
		exec.Command("systemctl", "daemon-reload").Run()
		return exec.Command("systemctl", "enable", "--now", "cftunnelX-relay").Run()
	default:
		return fmt.Errorf("当前平台不支持通过 Web 注册服务")
	}
}

func uninstallRelayService() error {
	switch runtime.GOOS {
	case "windows":
		hiddenCommand("sc", "stop", "cftunnelX-relay").Run()
		return hiddenCommand("sc", "delete", "cftunnelX-relay").Run()
	case "linux":
		exec.Command("systemctl", "disable", "--now", "cftunnelX-relay").Run()
		return os.Remove("/etc/systemd/system/cftunnelX-relay.service")
	default:
		return fmt.Errorf("当前平台不支持通过 Web 卸载服务")
	}
}

// ========== Relay 服务端（frps） ==========

func (s *Server) handleRelayServer(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// 查询 frps 服务状态
		supported := true
		installed := false
		running := false
		status := ""
		version := ""
		latestKnown := "0.66.0"

		if runtime.GOOS == "windows" {
			out, err := hiddenCommand("sc", "query", "frps").Output()
			if err == nil {
				installed = true
				if strings.Contains(string(out), "RUNNING") {
					running = true
				}
				status = strings.TrimSpace(string(out))
			}
		} else if runtime.GOOS == "linux" {
			out, err := exec.Command("systemctl", "is-active", "frps").Output()
			if err == nil {
				installed = true
				status = strings.TrimSpace(string(out))
				running = status == "active"
			}
		}
		if out, err := hiddenCommand(relay.FrpsPath(), "--version").CombinedOutput(); err == nil {
			version = strings.TrimSpace(string(out))
		} else if out, err := hiddenCommand("frps", "--version").CombinedOutput(); err == nil {
			version = strings.TrimSpace(string(out))
		}
		writeOK(w, map[string]interface{}{
			"supported":    supported,
			"installed":    installed,
			"running":      running,
			"status":       status,
			"version":      version,
			"latest_known": latestKnown,
		})

	case http.MethodPost:
		// 安装 frps 服务端
		var body struct {
			Port int `json:"port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "请求格式错误")
			return
		}
		if body.Port == 0 {
			body.Port = 7000
		}
		token, err := installFrpsServer(body.Port)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]interface{}{
			"status": "installed",
			"port":   body.Port,
			"token":  token,
		})

	case http.MethodDelete:
		// 卸载 frps
		if runtime.GOOS == "windows" {
			hiddenCommand("sc", "stop", "frps").Run()
			if err := hiddenCommand("sc", "delete", "frps").Run(); err != nil {
				writeError(w, 500, err.Error())
				return
			}
			frpsPath := relay.FrpsPath()
			os.Remove(frpsPath)
		} else if runtime.GOOS == "linux" {
			exec.Command("systemctl", "disable", "--now", "frps").Run()
			os.Remove("/etc/systemd/system/frps.service")
			os.Remove("/usr/local/bin/frps")
			os.RemoveAll("/etc/frps")
		} else {
			writeError(w, 400, "当前平台不支持卸载 frps 服务端")
			return
		}
		writeOK(w, map[string]string{"status": "uninstalled"})

	default:
		writeError(w, 405, "method not allowed")
	}
}

func installFrpsServer(port int) (string, error) {
	binPath, err := relay.EnsureFrps()
	if err != nil {
		return "", err
	}

	// 生成 token
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return "", fmt.Errorf("生成 token 失败: %w", err)
	}
	token := hex.EncodeToString(b)

	if runtime.GOOS == "windows" {
		// Windows: 配置文件放 bin 同目录
		configDir := filepath.Dir(binPath)
		configPath := filepath.Join(configDir, "frps.toml")
		configContent := fmt.Sprintf("bindPort = %d\nauth.token = %q\n", port, token)
		if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
			return "", err
		}
		// 注册 Windows 服务
		binArg := fmt.Sprintf(`%s -c %s`, binPath, configPath)
		if err := hiddenCommand("sc", "create", "frps", "binPath=", binArg, "start=", "auto").Run(); err != nil {
			return "", fmt.Errorf("创建服务失败: %w", err)
		}
		if err := hiddenCommand("sc", "start", "frps").Run(); err != nil {
			return "", fmt.Errorf("启动服务失败: %w", err)
		}
		return token, nil
	}

	// Linux: 安装到 /usr/local/bin
	destBin := "/usr/local/bin/frps"
	input, err := os.ReadFile(binPath)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(destBin, input, 0755); err != nil {
		return "", fmt.Errorf("复制 frps 失败（需要 sudo？）: %w", err)
	}
	configDir := "/etc/frps"
	os.MkdirAll(configDir, 0755)
	configPath := configDir + "/frps.toml"
	configContent := fmt.Sprintf("bindPort = %d\nauth.token = %q\n", port, token)
	if err := os.WriteFile(configPath, []byte(configContent), 0600); err != nil {
		return "", err
	}
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
	if err := os.WriteFile("/etc/systemd/system/frps.service", []byte(unit), 0644); err != nil {
		return "", err
	}
	exec.Command("systemctl", "daemon-reload").Run()
	if err := exec.Command("systemctl", "enable", "--now", "frps").Run(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Server) handleRelayServerSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Auth     string `json:"auth"`
		Password string `json:"password"`
		KeyPath  string `json:"key_path"`
		FrpsPort int    `json:"frps_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	body.Host = strings.TrimSpace(body.Host)
	body.User = strings.TrimSpace(body.User)
	body.KeyPath = strings.TrimSpace(body.KeyPath)
	if body.Host == "" {
		writeError(w, 400, "服务器地址不能为空")
		return
	}
	if body.Port == 0 {
		body.Port = 22
	}
	if body.User == "" {
		body.User = "root"
	}
	if body.FrpsPort == 0 {
		body.FrpsPort = 7000
	}
	sshCfg := &sshutil.ConnectConfig{
		Host: body.Host,
		Port: body.Port,
		User: body.User,
	}
	if body.Auth == "password" {
		sshCfg.Password = body.Password
	} else {
		sshCfg.KeyPath = body.KeyPath
	}
	client, err := sshutil.Connect(sshCfg)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	defer client.Close()
	if err := preflightRemoteFrps(client); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if err := runRemoteScript(client, buildRemoteFrpsInstallScript(body.FrpsPort)); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	token, err := sshutil.RunCommandOutput(client, `grep 'auth.token' /etc/frps/frps.toml | sed 's/.*"\(.*\)"/\1/'`)
	if err != nil || token == "" {
		writeError(w, 500, "frps 已安装，但无法读取远程 Token，请手动查看 /etc/frps/frps.toml")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	cfg.Relay.Server = fmt.Sprintf("%s:%d", body.Host, body.FrpsPort)
	cfg.Relay.Token = token
	if err := cfg.Save(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.cfg = cfg
	writeOK(w, map[string]interface{}{
		"status": "installed",
		"server": cfg.Relay.Server,
		"token":  token,
	})
}

func preflightRemoteFrps(client *ssh.Client) error {
	osName, err := sshutil.RunCommandOutput(client, "uname -s")
	if err != nil || osName != "Linux" {
		return fmt.Errorf("远程服务器不是 Linux (检测到: %s)", osName)
	}
	uid, err := sshutil.RunCommandOutput(client, "id -u")
	if err != nil || uid != "0" {
		return fmt.Errorf("需要 root 权限 (当前 uid: %s)", uid)
	}
	arch, err := sshutil.RunCommandOutput(client, "uname -m")
	if err != nil {
		return fmt.Errorf("无法检测架构: %w", err)
	}
	switch arch {
	case "x86_64", "aarch64", "armv7l":
	default:
		return fmt.Errorf("不支持的架构: %s", arch)
	}
	return nil
}

func runRemoteScript(client *ssh.Client, script string) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("创建 SSH session 失败: %w", err)
	}
	defer session.Close()
	return session.Run(script)
}

func buildRemoteFrpsInstallScript(bindPort int) string {
	return fmt.Sprintf(`set -euo pipefail
FRP_VERSION="0.66.0"
BIND_PORT=%d
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/frps"
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) FRP_ARCH="amd64" ;;
  aarch64) FRP_ARCH="arm64" ;;
  armv7l) FRP_ARCH="arm" ;;
  *) echo "[ERROR] 不支持的架构: $ARCH"; exit 1 ;;
esac
FILENAME="frp_${FRP_VERSION}_linux_${FRP_ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/${FILENAME}"
MIRRORS=("https://ghfast.top/" "https://gh-proxy.com/" "https://ghproxy.cn/" "")
TMP_DIR=$(mktemp -d)
trap "rm -rf $TMP_DIR" EXIT
download_ok=false
for mirror in "${MIRRORS[@]}"; do
  url="${mirror}${DOWNLOAD_URL}"
  if curl -fsSL --connect-timeout 10 -o "$TMP_DIR/$FILENAME" "$url"; then
    download_ok=true; break
  fi
done
[ "$download_ok" = false ] && echo "[ERROR] 所有下载源均失败" && exit 1
tar -xzf "$TMP_DIR/$FILENAME" -C "$TMP_DIR"
install -m 755 "$TMP_DIR/frp_${FRP_VERSION}_linux_${FRP_ARCH}/frps" "$INSTALL_DIR/frps"
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
`, bindPort)
}

// ========== 综合链路检测 ==========

func (s *Server) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	// 1. cloudflared 检测（只读）
	cfCheck := checkCloudflaredReadOnly()
	// 增加详细提示
	cfCheck["hint"] = getCloudflaredHint(cfCheck)

	// 2. Cloudflare API 检测
	apiCheck := map[string]interface{}{"reachable": false}
	if cfg.Auth.APIToken != "" {
		client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		start := time.Now()
		zones, err := client.ListZones(ctx)
		cancel()
		apiCheck["latency_ms"] = time.Since(start).Milliseconds()
		if err != nil {
			apiCheck["err"] = err.Error()
			apiCheck["hint"] = "API Token 无效或已过期，请前往 dash.cloudflare.com/profile/api-tokens 重新创建"
		} else {
			apiCheck["reachable"] = true
			apiCheck["zone_count"] = len(zones)
			apiCheck["hint"] = fmt.Sprintf("API 正常，账户下共 %d 个域名", len(zones))
		}
	} else {
		apiCheck["err"] = "API Token 未配置"
		apiCheck["hint"] = "请先在仪表盘配置 API Token 和 Account ID"
	}

	// 3. 收集所有路由（多隧道）
	var allRoutes []daemon.RouteInput
	tunnelGroups := make([]map[string]interface{}, 0, len(cfg.Tunnels))
	for _, t := range cfg.Tunnels {
		group := map[string]interface{}{
			"id":      t.ID,
			"name":    t.Name,
			"running": daemon.RunningTunnel(t.ID),
			"routes":  []map[string]interface{}{},
		}
		for _, r := range t.Routes {
			rt := daemon.RouteInput{Name: r.Name, Hostname: r.Hostname, Service: r.Service}
			allRoutes = append(allRoutes, rt)
			group["routes"] = append(group["routes"].([]map[string]interface{}), diagnoseRouteReadOnly(rt))
		}
		tunnelGroups = append(tunnelGroups, group)
	}
	for _, r := range cfg.Routes {
		allRoutes = append(allRoutes, daemon.RouteInput{
			Name: r.Name, Hostname: r.Hostname, Service: r.Service,
		})
	}

	// 4. 路由诊断（并行：本地端口 + DNS + 域名可达性）
	type routeResult struct {
		idx int
		r   map[string]interface{}
	}
	ch := make(chan routeResult, len(allRoutes))
	for i, rt := range allRoutes {
		go func(idx int, route daemon.RouteInput) {
			r := diagnoseRouteReadOnly(route)
			ch <- routeResult{idx, r}
		}(i, rt)
	}
	routeResults := make([]map[string]interface{}, len(allRoutes))
	for i := 0; i < len(allRoutes); i++ {
		rr := <-ch
		routeResults[rr.idx] = rr.r
	}

	passed := 0
	failed := 0
	for _, r := range routeResults {
		if r["local_ok"].(bool) && r["dns_ok"].(bool) {
			passed++
		} else {
			failed++
		}
	}
	relayCheck := relay.Check(&cfg.Relay, "")
	relayStatus := "未配置中继服务器"
	if cfg.Relay.Server == "" {
		relayStatus = "中继服务器未配置"
	} else if !relay.Running() {
		relayStatus = "中继服务未启动"
	} else if relayCheck.ServerOK {
		relayStatus = fmt.Sprintf("中继服务器可达 / %dms", relayCheck.ServerLatency)
	} else {
		relayStatus = "中继服务器不可达"
	}

	writeOK(w, map[string]interface{}{
		"cloudflared": cfCheck,
		"api":         apiCheck,
		"routes":      routeResults,
		"tunnels":     tunnelGroups,
		"relay": map[string]interface{}{
			"status":       relayStatus,
			"server":       relayCheck.Server,
			"server_ok":    relayCheck.ServerOK,
			"frpc_running": relayCheck.FrpcRunning,
			"rules":        relayCheck.Rules,
			"total":        relayCheck.Total,
			"passed":       relayCheck.Passed,
			"failed":       relayCheck.Failed,
		},
		"total":  len(allRoutes),
		"passed": passed,
		"failed": failed,
	})
}

// diagnoseRouteReadOnly 只读路由诊断
func diagnoseRouteReadOnly(route daemon.RouteInput) map[string]interface{} {
	r := map[string]interface{}{
		"name":     route.Name,
		"hostname": route.Hostname,
		"service":  route.Service,
		"local_ok": false,
		"dns_ok":   false,
		"http_ok":  false,
	}
	// 本地端口检测
	port := extractPortFromService(route.Service)
	if port != "" {
		conn, err := netDialTimeout("tcp", "127.0.0.1:"+port, 5*time.Second)
		if err == nil {
			conn.Close()
			r["local_ok"] = true
			r["local_hint"] = fmt.Sprintf("本地服务 127.0.0.1:%s 正常监听", port)
		} else {
			r["local_err"] = "未监听"
			r["local_hint"] = fmt.Sprintf("本地端口 %s 未监听，请确认服务已启动", port)
		}
	} else {
		r["local_err"] = "无法解析端口"
		r["local_hint"] = "Service 格式错误，应为 http://localhost:端口"
	}
	// DNS 检测
	if route.Hostname != "" {
		ips, err := netLookupHost(route.Hostname)
		if err == nil {
			r["dns_ok"] = true
			if len(ips) > 0 {
				r["dns_hint"] = fmt.Sprintf("DNS 解析正常，IP: %s", ips[0])
			}
		} else {
			r["dns_err"] = "解析失败"
			r["dns_hint"] = "DNS 解析失败，请检查 CNAME 是否已创建或等待 DNS 传播(可能需几分钟)"
		}
	}
	// HTTPS 可达性
	if route.Hostname != "" && r["dns_ok"].(bool) {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("https://" + route.Hostname)
		if err == nil {
			resp.Body.Close()
			r["http_ok"] = true
			r["http_hint"] = fmt.Sprintf("HTTPS 可达，状态码 %d", resp.StatusCode)
		} else {
			r["http_err"] = "不可达"
			r["http_hint"] = "HTTPS 不可达，请确认隧道已启动且 ingress 已推送"
		}
	}
	return r
}

// getCloudflaredHint 根据 cloudflared 状态返回提示
func getCloudflaredHint(cf map[string]interface{}) string {
	installed, _ := cf["installed"].(bool)
	running, _ := cf["running"].(bool)
	if !installed {
		return "cloudflared 未安装，请运行 'cftunnel up' 自动下载"
	}
	if running {
		return "cloudflared 运行中，隧道连接正常"
	}
	return "cloudflared 已安装但未运行，请启动隧道"
}

// ========== Web 面板配置 ==========

func (s *Server) handleWebPanel(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]interface{}{
			"username":       cfg.WebUI.Username,
			"has_password":   cfg.WebUI.Password != "",
			"has_username":   cfg.WebUI.Username != "",
			"port":           cfg.WebUI.Port,
			"theme":          cfg.WebUI.Theme,
			"remote_enabled": cfg.WebUI.RemoteEnabled,
			"remote_domain":  cfg.WebUI.RemoteDomain,
			"tunnel_name":    cfg.WebUI.TunnelName,
			"service_name":   cfg.WebUI.ServiceName,
		})

	case http.MethodPost:
		var body struct {
			Username      *string `json:"username"` // 指针：区分未传(nil)和传空字符串(关闭认证)
			Password      *string `json:"password"` // 指针：同上
			Theme         string  `json:"theme"`
			RemoteEnabled bool    `json:"remote_enabled"`
			RemoteDomain  string  `json:"remote_domain"`
			TunnelName    string  `json:"tunnel_name"`
			ServiceName   string  `json:"service_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "请求格式错误")
			return
		}

		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		// 指针判断：明确传值(含空字符串)才更新，支持留空关闭认证
		if body.Username != nil {
			cfg.WebUI.Username = *body.Username
		}
		if body.Password != nil {
			cfg.WebUI.Password = *body.Password
		}
		if body.Theme != "" {
			cfg.WebUI.Theme = body.Theme
		}
		cfg.WebUI.RemoteEnabled = body.RemoteEnabled
		cfg.WebUI.RemoteDomain = strings.TrimSpace(body.RemoteDomain)
		if body.TunnelName != "" {
			cfg.WebUI.TunnelName = body.TunnelName
		}
		if body.ServiceName != "" {
			cfg.WebUI.ServiceName = body.ServiceName
		} else if cfg.WebUI.ServiceName == "" {
			cfg.WebUI.ServiceName = "cftunnelX-web"
		}

		if err := cfg.Save(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.cfg = cfg
		writeOK(w, map[string]string{"status": "ok"})

	default:
		writeError(w, 405, "method not allowed")
	}
}

// ========== Zones 列表 ==========

func (s *Server) handleZones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if cfg.Auth.APIToken == "" {
		writeError(w, 400, "请先配置 API Token")
		return
	}

	client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	zones, err := client.ListZones(ctx)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	result := make([]map[string]string, 0, len(zones))
	for _, z := range zones {
		result = append(result, map[string]string{
			"id":   z.ID,
			"name": z.Name,
		})
	}
	writeOK(w, result)
}

// ========== 主题 ==========

func (s *Server) handleTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Theme string `json:"theme"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	cfg.WebUI.Theme = body.Theme
	if err := cfg.Save(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.cfg = cfg
	writeOK(w, map[string]string{"theme": body.Theme})
}

// ========== 版本信息 ==========

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeOK(w, map[string]string{
		"version": s.version,
		"go":      runtime.Version(),
		"os":      runtime.GOOS + "/" + runtime.GOARCH,
	})
}

// ========== 日志 ==========

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	logPath := logFilePath()
	if r.Method == http.MethodDelete {
		if err := os.WriteFile(logPath, []byte{}, 0644); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]string{"status": "cleared"})
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}

	limit := 200
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	lines, err := readLogTailLines(logPath, limit)
	if err != nil {
		writeOK(w, map[string]interface{}{
			"lines": []string{},
			"path":  logPath,
		})
		return
	}
	writeOK(w, map[string]interface{}{
		"lines": lines,
		"path":  logPath,
	})
}

// ========== Cloud 系统服务（install/uninstall） ==========

func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		svc := service.New()
		running := svc.Running()
		writeOK(w, map[string]interface{}{
			"installed": svc.Installed(),
			"running":   running,
		})

	case http.MethodPost:
		cfg, err := config.Load()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		if cfg.Tunnel.Token == "" {
			writeError(w, 400, "未配置隧道")
			return
		}
		binPath, err := daemon.EnsureCloudflared()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		svc := service.New()
		if err := svc.Install(binPath, cfg.Tunnel.Token); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]string{"status": "installed"})

	case http.MethodDelete:
		svc := service.New()
		if err := svc.Uninstall(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]string{"status": "uninstalled"})

	default:
		writeError(w, 405, "method not allowed")
	}
}

// ========== cftunnel 软件开机自启动 ==========

func (s *Server) handleAppStartup(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeOK(w, appStartupStatus())
	case http.MethodPost:
		if err := appStartupInstall(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]string{"status": "installed"})
	case http.MethodDelete:
		if err := appStartupUninstall(); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeOK(w, map[string]string{"status": "uninstalled"})
	default:
		writeError(w, 405, "method not allowed")
	}
}

// ========== 终端执行 ==========

func (s *Server) handleTerminalExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Cmd string `json:"cmd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	cmd := strings.TrimSpace(body.Cmd)
	if cmd == "" {
		writeError(w, 400, "命令不能为空")
		return
	}

	// 安全限制：禁止危险命令
	dangerous := []string{"rm -rf /", "mkfs", "dd if=", ":(){ :|:&", "format c:"}
	for _, d := range dangerous {
		if strings.Contains(cmd, d) {
			writeError(w, 400, "禁止执行危险命令")
			return
		}
	}

	// 使用 cftunnel 自身执行子命令
	var exe string
	var args []string
	if strings.HasPrefix(cmd, "cftunnelX ") || strings.HasPrefix(cmd, "cftunnelx ") || strings.HasPrefix(cmd, "cftunnel ") {
		exe = selfExePath()
		raw := cmd
		for _, prefix := range []string{"cftunnelX ", "cftunnelx ", "cftunnel "} {
			if strings.HasPrefix(raw, prefix) {
				raw = strings.TrimPrefix(raw, prefix)
				break
			}
		}
		args = strings.Fields(raw)
	} else {
		// 普通 shell 命令
		exe = "sh"
		if runtime.GOOS == "windows" {
			exe = "cmd"
			args = []string{"/c", cmd}
		} else {
			args = []string{"-c", cmd}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	execCmd := exec.CommandContext(ctx, exe, args...)
	configureHiddenCommand(execCmd)
	out, err := execCmd.CombinedOutput()
	output := string(out)
	if err != nil {
		output += fmt.Sprintf("\n[退出错误: %v]", err)
	}
	writeOK(w, map[string]interface{}{
		"output": output,
		"cmd":    cmd,
	})
}

// selfExePath 返回当前可执行文件路径
func selfExePath() string {
	exe, _ := os.Executable()
	return exe
}

// ========== 快捷命令列表 ==========

func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	commands := []map[string]string{
		{"cmd": "cftunnelX init", "desc": "初始配置 API Token 和 Account ID"},
		{"cmd": "cftunnelX status", "desc": "查看隧道状态"},
		{"cmd": "cftunnelX create <名称>", "desc": "创建隧道"},
		{"cmd": "cftunnelX add <服务名> <端口> -domain <域名>", "desc": "添加路由"},
		{"cmd": "cftunnelX up", "desc": "启动隧道"},
		{"cmd": "cftunnelX down", "desc": "停止隧道"},
		{"cmd": "cftunnelX list", "desc": "列出所有路由"},
		{"cmd": "cftunnelX diagnose", "desc": "诊断链路"},
		{"cmd": "cftunnelX quick <端口>", "desc": "免域名快速启动"},
		{"cmd": "cftunnelX relay init", "desc": "配置中继服务器"},
		{"cmd": "cftunnelX relay add <名称> <本地端口> <远程端口>", "desc": "添加中继规则"},
		{"cmd": "cftunnelX relay up", "desc": "启动中继"},
		{"cmd": "cftunnelX relay check", "desc": "检测中继链路"},
		{"cmd": "cftunnelX relay install", "desc": "注册中继系统服务"},
		{"cmd": "cftunnelX relay server install", "desc": "安装 frps 服务端（仅Linux）"},
		{"cmd": "cftunnelX install", "desc": "注册 Cloud 隧道系统服务"},
		{"cmd": "cftunnelX logs", "desc": "查看日志"},
		{"cmd": "cftunnelX version", "desc": "查看版本"},
	}
	writeOK(w, commands)
}

// OpenBrowser 打开浏览器
func (s *Server) OpenBrowser(url string) {
	_ = openBrowserURL(url)
}

// deleteRouteFromConfig 从配置中删除路由（支持多隧道和单隧道）
// 同时清理 DNS 记录
// 返回: 隧道ID, 剩余路由列表, 是否找到
func deleteRouteFromConfig(cfg *config.Config, name string) (tunnelID string, remaining []config.RouteConfig, found bool, err error) {
	client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
	// 遍历多隧道
	for i := range cfg.Tunnels {
		for j := range cfg.Tunnels[i].Routes {
			if cfg.Tunnels[i].Routes[j].Name == name {
				r := cfg.Tunnels[i].Routes[j]
				if r.DNSRecordID != "" && r.ZoneID != "" {
					if err := client.DeleteDNSRecord(context.Background(), r.ZoneID, r.DNSRecordID); err != nil {
						return "", nil, false, err
					}
				}
				cfg.Tunnels[i].Routes = append(cfg.Tunnels[i].Routes[:j], cfg.Tunnels[i].Routes[j+1:]...)
				return cfg.Tunnels[i].ID, cfg.Tunnels[i].Routes, true, nil
			}
		}
	}
	// 向后兼容单隧道
	r := cfg.FindRoute(name)
	if r == nil {
		return "", nil, false, nil
	}
	if r.DNSRecordID != "" && r.ZoneID != "" {
		if err := client.DeleteDNSRecord(context.Background(), r.ZoneID, r.DNSRecordID); err != nil {
			return "", nil, false, err
		}
	}
	cfg.RemoveRoute(name)
	return cfg.Tunnel.ID, cfg.Routes, true, nil
}

// ========== 批量路由导入 ==========

func (s *Server) handleRoutesBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		TunnelID string                   `json:"tunnel_id"`
		Routes   []map[string]interface{} `json:"routes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	var tunnel *config.TunnelConfig
	if body.TunnelID != "" {
		tunnel = cfg.FindTunnel(body.TunnelID)
	} else if len(cfg.Tunnels) > 0 {
		tunnel = &cfg.Tunnels[0]
	}
	if tunnel == nil {
		writeError(w, 400, "请先创建隧道")
		return
	}
	client := cfapi.New(cfg.Auth.APIToken, cfg.Auth.AccountID)
	ctx := context.Background()
	added := 0
	failed := 0
	for _, rr := range body.Routes {
		name, _ := rr["name"].(string)
		port, _ := rr["port"].(string)
		domain, _ := rr["domain"].(string)
		if name == "" || port == "" || domain == "" {
			failed++
			continue
		}
		if cfg.FindRoute(name) != nil {
			failed++
			continue
		}
		zone, err := findZoneForDomain(client, ctx, domain)
		if err != nil {
			failed++
			continue
		}
		target := tunnel.ID + ".cfargotunnel.com"
		recordID, err := client.CreateCNAME(ctx, zone.ID, domain, target)
		if err != nil {
			failed++
			continue
		}
		tunnel.Routes = append(tunnel.Routes, config.RouteConfig{
			Name: name, Hostname: domain, Service: "http://localhost:" + port,
			ZoneID: zone.ID, DNSRecordID: recordID,
		})
		added++
	}
	if err := cfg.Save(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// 推送 ingress
	var rules []cfapi.IngressRule
	for _, r := range tunnel.Routes {
		rules = append(rules, cfapi.IngressRule{Hostname: r.Hostname, Service: r.Service})
	}
	if err := client.PushIngressConfig(ctx, tunnel.ID, rules); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.cfg = cfg
	writeOK(w, map[string]interface{}{"added": added, "failed": failed})
}

// ========== 端口修改 ==========

func (s *Server) handlePort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	var body struct {
		Port string `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "请求格式错误")
		return
	}
	port := strings.TrimSpace(body.Port)
	if port == "" {
		writeError(w, 400, "端口不能为空")
		return
	}
	if _, err := strconv.Atoi(port); err != nil {
		writeError(w, 400, "端口必须是数字")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	cfg.WebUI.Port = port
	if err := cfg.Save(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeOK(w, map[string]interface{}{
		"status":  "ok",
		"port":    port,
		"message": "端口已保存，需要重启程序生效",
	})
}

// ========== 日志时间戳工具 ==========

// ts 返回带时间戳的日志前缀 [2026-07-03 23:05:40]
func ts() string {
	return logutil.Timestamp()
}

// logLine 向日志文件写入一行带时间戳的日志
func logLine(msg string, args ...interface{}) {
	logutil.Write(logFilePath(), "INFO", msg, args...)
}

func modeName() string {
	if config.Portable() {
		return "relative"
	}
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "service"
}

func localIPs() []string {
	seen := map[string]bool{}
	ips := make([]string, 0, 4)
	if conn, err := net.DialTimeout("udp", "8.8.8.8:80", 500*time.Millisecond); err == nil {
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
			if ip := addr.IP.To4(); ip != nil && usableLocalIP(ip) {
				ips = append(ips, ip.String())
				seen[ip.String()] = true
			}
		}
		conn.Close()
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP == nil || ipNet.IP.IsLoopback() {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		s := ip.String()
		if usableLocalIP(ip) && !seen[s] {
			ips = append(ips, s)
			seen[s] = true
		}
	}
	return ips
}

func usableLocalIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip[0] == 169 && ip[1] == 254 {
		return false
	}
	return true
}

func logLineCount(path string) int {
	lines, err := readLogTailLines(path, 1000)
	if err != nil {
		return 0
	}
	return len(lines)
}

func readLogTailLines(path string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	readSize := int64(limit * 512)
	if readSize < 64*1024 {
		readSize = 64 * 1024
	}
	if readSize > 1024*1024 {
		readSize = 1024 * 1024
	}
	if readSize > stat.Size() {
		readSize = stat.Size()
	}
	offset := stat.Size() - readSize
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lines := make([]string, 0, limit)
	first := offset > 0
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		line := logutil.NormalizeLine(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) > limit {
			copy(lines, lines[len(lines)-limit:])
			lines = lines[:limit]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}
