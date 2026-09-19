package ingress

import (
	"testing"

	"github.com/shiranzby/cftunnelX/internal/config"
)

func tunnelWith(routes ...config.RouteConfig) *config.TunnelConfig {
	return &config.TunnelConfig{ID: "t1", Name: "t", Token: "tok", Routes: routes}
}

// TestRulesMapsAuthRouteToProxy 是本项安全缺陷的核心回归测试。
//
// 缺陷：Web 面板路径构造 ingress 时直接用了路由的源站 service，
// 于是"配了认证的路由"在公网可以直接 200 访问且不要密码——认证被静默绕过。
// 正确行为：启用鉴权的路由必须指向本地鉴权代理端口。
func TestRulesMapsAuthRouteToProxy(t *testing.T) {
	tun := tunnelWith(
		config.RouteConfig{Name: "plain", Hostname: "a.example.com", Service: "http://localhost:3000"},
		config.RouteConfig{
			Name: "protected", Hostname: "b.example.com", Service: "http://localhost:5250",
			Auth: &config.AuthProxy{Username: "u", Password: "p"},
		},
	)

	rules := Rules(tun)
	if len(rules) != 2 {
		t.Fatalf("规则数 = %d, 期望 2", len(rules))
	}

	byHost := map[string]string{}
	for _, r := range rules {
		byHost[r.Hostname] = r.Service
	}

	if got := byHost["a.example.com"]; got != "http://localhost:3000" {
		t.Errorf("未启用鉴权的路由应保持源站地址, 实际 %q", got)
	}
	// 5250 + 1 = 5251
	if got := byHost["b.example.com"]; got != "http://127.0.0.1:5251" {
		t.Errorf("启用鉴权的路由必须指向鉴权代理端口, 实际 %q", got)
	}
}

// TestRulesBypassAuthExplicit 只有显式指定 BypassAuth 时才允许指向源站。
func TestRulesBypassAuthExplicit(t *testing.T) {
	tun := tunnelWith(config.RouteConfig{
		Name: "protected", Hostname: "b.example.com", Service: "http://localhost:5250",
		Auth: &config.AuthProxy{Username: "u", Password: "p"},
	})

	rules := RulesWith(tun, Options{BypassAuth: true})
	if len(rules) != 1 {
		t.Fatalf("规则数 = %d", len(rules))
	}
	if rules[0].Service != "http://localhost:5250" {
		t.Errorf("显式绕过时应指向源站, 实际 %q", rules[0].Service)
	}
}

// TestAuthProxyPortDeterministic 代理端口必须确定，否则 ingress 会与监听端口不一致。
func TestAuthProxyPortDeterministic(t *testing.T) {
	cases := []struct {
		name  string
		route config.RouteConfig
		want  int
	}{
		{
			name:  "未启用鉴权",
			route: config.RouteConfig{Service: "http://localhost:5250"},
			want:  0,
		},
		{
			name: "从目标端口推导",
			route: config.RouteConfig{Service: "http://localhost:5250",
				Auth: &config.AuthProxy{Username: "u", Password: "p"}},
			want: 5251,
		},
		{
			name: "显式 ListenPort 优先",
			route: config.RouteConfig{Service: "http://localhost:5250",
				Auth: &config.AuthProxy{Username: "u", Password: "p", ListenPort: 9000}},
			want: 9000,
		},
		{
			name: "https 前缀同样可解析",
			route: config.RouteConfig{Service: "https://127.0.0.1:8080",
				Auth: &config.AuthProxy{Username: "u", Password: "p"}},
			want: 8081,
		},
		{
			name: "service 无端口时无法推导",
			route: config.RouteConfig{Service: "http://localhost",
				Auth: &config.AuthProxy{Username: "u", Password: "p"}},
			want: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.route.AuthProxyPort(); got != tc.want {
				t.Errorf("AuthProxyPort() = %d, 期望 %d", got, tc.want)
			}
		})
	}
}

// TestAuthEnabledRequiresBothCredentials 凭据不完整不算启用鉴权，
// 但也不能因此把源站直接暴露出去——调用方需配合校验（见 CheckAuthProxies 的用法）。
func TestAuthEnabledRequiresBothCredentials(t *testing.T) {
	cases := []struct {
		name string
		auth *config.AuthProxy
		want bool
	}{
		{"两者齐全", &config.AuthProxy{Username: "u", Password: "p"}, true},
		{"只有用户名", &config.AuthProxy{Username: "u"}, false},
		{"只有密码", &config.AuthProxy{Password: "p"}, false},
		{"都没有", &config.AuthProxy{}, false},
		{"没有 auth 段", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (config.RouteConfig{Auth: tc.auth}).AuthEnabled(); got != tc.want {
				t.Errorf("AuthEnabled() = %v, 期望 %v", got, tc.want)
			}
		})
	}
}

// TestRulesSkipsEmptyHostname 没有域名的路由不应产生 ingress 规则。
func TestRulesSkipsEmptyHostname(t *testing.T) {
	tun := tunnelWith(
		config.RouteConfig{Name: "nohost", Service: "http://localhost:3000"},
		config.RouteConfig{Name: "ok", Hostname: "a.example.com", Service: "http://localhost:3000"},
	)
	rules := Rules(tun)
	if len(rules) != 1 || rules[0].Hostname != "a.example.com" {
		t.Fatalf("应只保留带域名的路由, 实际 %+v", rules)
	}
}

// TestProxyPortsDedup 端口去重且升序。
func TestProxyPortsDedup(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tunnels = []config.TunnelConfig{*tunnelWith(
		config.RouteConfig{Name: "a", Hostname: "a.example.com", Service: "http://localhost:5250",
			Auth: &config.AuthProxy{Username: "u", Password: "p"}},
		config.RouteConfig{Name: "b", Hostname: "b.example.com", Service: "http://localhost:5250",
			Auth: &config.AuthProxy{Username: "u", Password: "p"}},
		config.RouteConfig{Name: "c", Hostname: "c.example.com", Service: "http://localhost:6000",
			Auth: &config.AuthProxy{Username: "u", Password: "p"}},
	)}
	ports := ProxyPorts(cfg)
	if len(ports) != 2 || ports[0] != 5251 || ports[1] != 6001 {
		t.Errorf("ProxyPorts = %v, 期望 [5251 6001]", ports)
	}
}
