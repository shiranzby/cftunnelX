package ingress

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shiranzby/cftunnelX/internal/authproxy"
	"github.com/shiranzby/cftunnelX/internal/config"
)

// 本文件覆盖缺陷 CFX-AUTHPROXY-INGRESS-001：
// 「代理没起来 / 端口被别的服务占用」时，仍按配置把 ingress 的 service
// 改写到那个端口，导致公网流量被导向无关服务且完全不经认证。
//
// 修复后的约定：BuildRules 是推送前的唯一入口，代理未真实就绪就 fail-closed。

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// startRealProxy 在指定端口起一个真实的鉴权代理，返回停止函数。
func startRealProxy(t *testing.T, port int) func() {
	t.Helper()
	p, err := authproxy.NewOnPort(authproxy.Config{
		Username: "u", Password: "p", TargetPort: "9", CookieTTL: time.Hour,
	}, port)
	if err != nil {
		t.Fatalf("创建鉴权代理失败: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("启动鉴权代理失败: %v", err)
	}
	// 等它真正进入监听，避免探活抢跑
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if authproxy.Probe(port) {
			return func() { _ = p.Stop() }
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = p.Stop()
	t.Fatalf("鉴权代理端口 %d 未就绪", port)
	return nil
}

// TestBuildRulesRejectsWhenProxyNotReady 代理没起来时必须拒绝推送。
func TestBuildRulesRejectsWhenProxyNotReady(t *testing.T) {
	port := freePort(t)
	tun := tunnelWith(config.RouteConfig{
		Name: "pcpanel", Hostname: "root.example.com", Service: "http://localhost:5250",
		Auth: &config.AuthProxy{Username: "u", Password: "p", ListenPort: port},
	})

	rules, err := BuildRules(tun)
	if err == nil {
		t.Fatalf("代理未就绪时应报错，实际返回 %d 条规则: %v", len(rules), rules)
	}
	if len(rules) != 0 {
		t.Fatal("失败时不得返回任何规则（否则调用方可能照样推出去）")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(port)) {
		t.Fatalf("错误信息应包含端口号 %d，实际: %v", port, err)
	}
}

// TestBuildRulesRejectsForeignListener 端口被别的服务占用时同样必须拒绝。
//
// 这是本次事故的精确复刻：端口在监听，但监听者不是我们的代理。
func TestBuildRulesRejectsForeignListener(t *testing.T) {
	port := freePort(t)
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer func() { _ = ln.Close() }()

	tun := tunnelWith(config.RouteConfig{
		Name: "pcpanel", Hostname: "root.example.com", Service: "http://localhost:5250",
		Auth: &config.AuthProxy{Username: "u", Password: "p", ListenPort: port},
	})

	rules, err := BuildRules(tun)
	if err == nil {
		t.Fatalf("端口被外部占用时必须报错，实际返回 %d 条规则", len(rules))
	}
	if len(rules) != 0 {
		t.Fatal("失败时不得返回任何规则")
	}
}

// TestBuildRulesAcceptsReadyProxy 代理就绪时正常生成规则，且 service 指向代理。
func TestBuildRulesAcceptsReadyProxy(t *testing.T) {
	port := freePort(t)
	stop := startRealProxy(t, port)
	defer stop()

	tun := tunnelWith(
		config.RouteConfig{Name: "plain", Hostname: "a.example.com", Service: "http://localhost:3000"},
		config.RouteConfig{
			Name: "protected", Hostname: "b.example.com", Service: "http://localhost:5250",
			Auth: &config.AuthProxy{Username: "u", Password: "p", ListenPort: port},
		},
	)

	rules, err := BuildRules(tun)
	if err != nil {
		t.Fatalf("代理就绪时不应报错: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("规则数 = %d，期望 2", len(rules))
	}

	want := "http://127.0.0.1:" + strconv.Itoa(port)
	byHost := map[string]string{}
	for _, r := range rules {
		byHost[r.Hostname] = r.Service
	}
	if byHost["b.example.com"] != want {
		t.Fatalf("鉴权路由 service = %q，期望 %q", byHost["b.example.com"], want)
	}
	// 未启用鉴权的路由必须原样保留（回归：不要把所有路由都改写到代理）
	if byHost["a.example.com"] != "http://localhost:3000" {
		t.Fatalf("无鉴权路由被改写了: %q", byHost["a.example.com"])
	}
}

// TestBuildRulesNoAuthUnchanged 没有 auth 的路由行为完全不变。
func TestBuildRulesNoAuthUnchanged(t *testing.T) {
	tun := tunnelWith(
		config.RouteConfig{Name: "a", Hostname: "a.example.com", Service: "http://localhost:3000"},
		config.RouteConfig{Name: "b", Hostname: "b.example.com", Service: "http://127.0.0.1:8080"},
	)

	rules, err := BuildRules(tun)
	if err != nil {
		t.Fatalf("无鉴权路由不应报错: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("规则数 = %d，期望 2", len(rules))
	}
	if rules[0].Service != "http://localhost:3000" || rules[1].Service != "http://127.0.0.1:8080" {
		t.Fatalf("无鉴权路由的 service 不应被改写: %+v", rules)
	}
}

// TestBuildRulesSkipsEmptyHostname 空 hostname 仍被跳过（保持既有行为）。
func TestBuildRulesSkipsEmptyHostname(t *testing.T) {
	tun := tunnelWith(
		config.RouteConfig{Name: "nohost", Service: "http://localhost:3000"},
		config.RouteConfig{Name: "ok", Hostname: "a.example.com", Service: "http://localhost:3000"},
	)
	rules, err := BuildRules(tun)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(rules) != 1 || rules[0].Hostname != "a.example.com" {
		t.Fatalf("空 hostname 应被跳过，实际: %+v", rules)
	}
}

// TestBuildRulesForConfigCoversAllTunnels 配置级入口必须覆盖所有隧道，
// 不能因为"活动隧道没有鉴权路由"就漏掉另一条隧道里的鉴权路由。
func TestBuildRulesForConfigCoversAllTunnels(t *testing.T) {
	port := freePort(t) // 故意不就绪

	cfg := &config.Config{
		Tunnels: []config.TunnelConfig{
			{ID: "t1", Name: "t1", Routes: []config.RouteConfig{
				{Name: "plain", Hostname: "a.example.com", Service: "http://localhost:3000"},
			}},
			{ID: "t2", Name: "t2", Routes: []config.RouteConfig{
				{Name: "protected", Hostname: "b.example.com", Service: "http://localhost:5250",
					Auth: &config.AuthProxy{Username: "u", Password: "p", ListenPort: port}},
			}},
		},
	}

	if _, err := BuildRulesForConfig(cfg); err == nil {
		t.Fatal("另一条隧道里的鉴权路由代理未就绪时，配置级入口也必须报错")
	}
}
