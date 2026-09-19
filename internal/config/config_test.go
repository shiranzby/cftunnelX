package config

import (
	"os"
	"testing"
)

// writeFixture 把配置写入当前测试进程的配置目录。
// go test 时 os.Executable() 指向临时目录下的测试二进制，
// 因此 Dir() 就是 <t.TempDir>/config，可安全写入。
func writeFixture(t *testing.T, body string) *Config {
	t.Helper()
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatalf("创建配置目录失败: %v", err)
	}
	if err := os.WriteFile(Path(), []byte(body), 0600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	return cfg
}

const legacySingleTunnel = `version: 1
auth:
  api_token: TOKEN-X
  account_id: ACC-X
tunnel:
  id: tunnel-id-123
  name: my-tunnel
  token: TUNNEL-SECRET-ABC
routes:
  - name: web
    hostname: a.example.com
    service: http://localhost:3000
`

const multiTunnel = `version: 1
auth:
  api_token: TOKEN-X
  account_id: ACC-X
tunnels:
  - id: tunnel-id-123
    name: my-tunnel
    token: TUNNEL-SECRET-ABC
    routes:
      - name: web
        hostname: a.example.com
        service: http://localhost:3000
`

// TestActiveTunnelLegacyShape 旧单隧道配置迁移后必须仍能取到隧道。
// 这是 v4.92 的 P0 缺陷：migrateSingleTunnel 清空了 Tunnel 字段，
// 而 cmd/up.go、cmd/install.go 只读该字段，导致服务注册与启动永久失败。
func TestActiveTunnelLegacyShape(t *testing.T) {
	cfg := writeFixture(t, legacySingleTunnel)

	if cfg.ActiveTunnel() == nil {
		t.Fatal("ActiveTunnel() 返回 nil，旧单隧道配置无法被识别")
	}
	if got := cfg.ActiveTunnelID(); got != "tunnel-id-123" {
		t.Errorf("ActiveTunnelID() = %q, 期望 tunnel-id-123", got)
	}
	if got := cfg.ActiveToken(); got != "TUNNEL-SECRET-ABC" {
		t.Errorf("ActiveToken() = %q, 期望 TUNNEL-SECRET-ABC", got)
	}
	if got := cfg.ActiveTunnelName(); got != "my-tunnel" {
		t.Errorf("ActiveTunnelName() = %q, 期望 my-tunnel", got)
	}
	if routes := cfg.ActiveRoutes(); len(routes) != 1 || routes[0].Name != "web" {
		t.Errorf("ActiveRoutes() = %+v, 期望 1 条名为 web 的路由", routes)
	}
}

// TestActiveTunnelArrayShape 数组形态配置同样必须可用。
func TestActiveTunnelArrayShape(t *testing.T) {
	cfg := writeFixture(t, multiTunnel)

	if got := cfg.ActiveToken(); got != "TUNNEL-SECRET-ABC" {
		t.Errorf("ActiveToken() = %q, 期望 TUNNEL-SECRET-ABC", got)
	}
	if routes := cfg.ActiveRoutes(); len(routes) != 1 {
		t.Errorf("ActiveRoutes() 长度 = %d, 期望 1", len(routes))
	}
}

// TestActiveTunnelEmpty 无隧道配置时应返回 nil 而不是崩溃。
func TestActiveTunnelEmpty(t *testing.T) {
	cfg := writeFixture(t, "version: 1\nauth:\n  api_token: T\n  account_id: A\n")

	if cfg.ActiveTunnel() != nil {
		t.Error("无隧道配置时 ActiveTunnel() 应为 nil")
	}
	if cfg.ActiveToken() != "" {
		t.Error("无隧道配置时 ActiveToken() 应为空串")
	}
	if routes := cfg.ActiveRoutes(); len(routes) != 0 {
		t.Errorf("无隧道配置时 ActiveRoutes() 应为空, 实际 %d 条", len(routes))
	}
}

// TestAddActiveRoute 添加路由必须落到 Tunnels 里并被持久化。
func TestAddActiveRoute(t *testing.T) {
	cfg := writeFixture(t, legacySingleTunnel)

	ok := cfg.AddActiveRoute(RouteConfig{
		Name:     "api",
		Hostname: "b.example.com",
		Service:  "http://localhost:8080",
	})
	if !ok {
		t.Fatal("AddActiveRoute 返回 false，应有可用隧道")
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	routes := reloaded.ActiveRoutes()
	if len(routes) != 2 {
		t.Fatalf("重新加载后路由数 = %d, 期望 2", len(routes))
	}
	if routes[1].Name != "api" {
		t.Errorf("新增路由未持久化: %+v", routes[1])
	}
}

// TestRemoveActiveTunnel 移除后不应再有可用隧道。
func TestRemoveActiveTunnel(t *testing.T) {
	cfg := writeFixture(t, multiTunnel)

	if !cfg.RemoveActiveTunnel() {
		t.Fatal("RemoveActiveTunnel 返回 false, 期望 true")
	}
	if cfg.ActiveTunnel() != nil {
		t.Error("移除后 ActiveTunnel() 应为 nil")
	}
}

// TestAddActiveRouteWithoutTunnel 无隧道时不应 panic。
func TestAddActiveRouteWithoutTunnel(t *testing.T) {
	cfg := writeFixture(t, "version: 1\n")
	if cfg.AddActiveRoute(RouteConfig{Name: "x"}) {
		t.Error("无隧道时 AddActiveRoute 应返回 false")
	}
}
