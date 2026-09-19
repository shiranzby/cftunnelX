package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// TestResolveListenHost 验证监听地址策略。
//
// 关键场景：OpenWrt / 服务器是无图形界面环境，必须监听 0.0.0.0，
// 否则用户从局域网打不开管理面板（这正是 Linux/OpenWrt "无法使用"的一环）。
func TestResolveListenHost(t *testing.T) {
	cases := []struct {
		name     string
		listen   string
		remote   bool
		headless bool
		override string
		want     string
	}{
		{name: "桌面环境默认回环", want: "127.0.0.1"},
		{name: "无图形界面监听所有网卡", headless: true, want: "0.0.0.0"},
		{name: "开启远程访问监听所有网卡", remote: true, want: "0.0.0.0"},
		{name: "命令行覆盖优先", headless: true, override: "192.168.1.10", want: "192.168.1.10"},
		{name: "配置显式指定优先", listen: "10.0.0.5", headless: true, want: "10.0.0.5"},
		{name: "配置为 auto 时走自动判定", listen: "auto", headless: true, want: "0.0.0.0"},
		{name: "配置为 auto 时桌面回环", listen: "auto", want: "127.0.0.1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.WebUI.Listen = tc.listen
			cfg.WebUI.RemoteEnabled = tc.remote

			got := resolveListenHostFor(cfg, tc.override, tc.headless)
			if got != tc.want {
				t.Errorf("resolveListenHostFor() = %q, 期望 %q", got, tc.want)
			}
		})
	}
}

func TestResolveListenHostNilConfig(t *testing.T) {
	if got := resolveListenHostFor(nil, "", false); got != "127.0.0.1" {
		t.Errorf("nil 配置应回退到回环地址, 实际 %q", got)
	}
	if got := resolveListenHostFor(nil, "", true); got != "0.0.0.0" {
		t.Errorf("nil 配置 + headless 应监听所有网卡, 实际 %q", got)
	}
}

// TestIsHeadlessFor Linux 无 X11/Wayland 时视为 headless；桌面系统始终有界面。
func TestIsHeadlessFor(t *testing.T) {
	cases := []struct {
		goos     string
		display  string
		wayland  string
		wantHead bool
	}{
		{goos: "windows", wantHead: false},
		{goos: "darwin", wantHead: false},
		{goos: "linux", wantHead: true},
		{goos: "linux", display: ":0", wantHead: false},
		{goos: "linux", wayland: "wayland-0", wantHead: false},
	}

	for _, tc := range cases {
		if got := isHeadlessFor(tc.goos, tc.display, tc.wayland); got != tc.wantHead {
			t.Errorf("isHeadlessFor(%q, %q, %q) = %v, 期望 %v",
				tc.goos, tc.display, tc.wayland, got, tc.wantHead)
		}
	}
}

// TestAuthAllowsPrivateLANWithoutCredentials 未配置账号密码时，
// 局域网来源应放行（OpenWrt 从 PC 打开面板的场景）。
func TestAuthAllowsPrivateLANWithoutCredentials(t *testing.T) {
	srv := newAuthTestServer(t, "version: 1\nweb_ui:\n  port: \"7860\"\n  remote_enabled: false\n")
	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, addr := range []string{"192.168.1.50:51234", "10.0.0.7:51234", "172.16.5.5:51234", "127.0.0.1:51234"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, reqFrom(addr, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("内网来源 %s 应放行, 实际 %d", addr, rec.Code)
		}
	}
}

// TestAuthDeniesPublicWithoutCredentials 未配置账号密码时，公网来源必须拒绝。
func TestAuthDeniesPublicWithoutCredentials(t *testing.T) {
	srv := newAuthTestServer(t, "version: 1\nweb_ui:\n  port: \"7860\"\n  remote_enabled: false\n")
	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, reqFrom("203.0.113.9:51234", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("公网来源应被拒绝, 实际 %d", rec.Code)
	}
}
