package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// TestSanitizeRoutesHidesPassword 路由查询接口不得回显明文密码。
//
// 背景：面板在未配置管理账号时是默认无认证的，任何能访问 /api/routes 的
// 人都能把路由的 auth 密码读走。设备端实测确认过该接口会返回
// {"Username":"...","Password":"..."}。
func TestSanitizeRoutesHidesPassword(t *testing.T) {
	const secret = "SuperSecretPassword-0618123"
	cfg := &config.Config{}
	cfg.Tunnels = []config.TunnelConfig{{
		ID: "t1", Name: "t", Token: "tok",
		Routes: []config.RouteConfig{{
			Name: "protected", Hostname: "a.example.com", Service: "http://localhost:5250",
			Auth: &config.AuthProxy{
				Username: "shiran", Password: secret, SigningKey: "88003e59deadbeef",
			},
		}},
	}}

	body, err := json.Marshal(sanitizeRoutes(cfg))
	if err != nil {
		t.Fatal(err)
	}
	raw := string(body)

	if strings.Contains(raw, secret) {
		t.Fatalf("响应中包含明文密码:\n%s", raw)
	}
	if strings.Contains(raw, "88003e59deadbeef") {
		t.Fatalf("响应中包含签名密钥:\n%s", raw)
	}
	// 但必须保留"是否已设置密码"的判断依据，否则界面无法显示状态
	if !strings.Contains(raw, `"has_password":true`) {
		t.Errorf("应保留 has_password 字段:\n%s", raw)
	}
	if !strings.Contains(raw, `"username":"shiran"`) {
		t.Errorf("应保留用户名:\n%s", raw)
	}
	if !strings.Contains(raw, `"listen_port":5251`) {
		t.Errorf("应暴露鉴权代理端口，便于排障:\n%s", raw)
	}
}

// TestAuthMiddlewareRejectsPartialCredentials 只配一半凭据必须拒绝，而不是静默放行。
//
// 旧行为：username 为空、password 有值 → hasWebCredentials 为 false →
// 落到"未配置凭据"分支 → 内网来源直接放行。用户以为有密码保护，实际没有。
func TestAuthMiddlewareRejectsPartialCredentials(t *testing.T) {
	cases := []struct {
		name    string
		cfgYAML string
	}{
		{
			name:    "只设置密码",
			cfgYAML: "version: 1\nweb_ui:\n  port: \"7860\"\n  password: onlypass\n",
		},
		{
			name:    "只设置用户名",
			cfgYAML: "version: 1\nweb_ui:\n  port: \"7860\"\n  username: onlyuser\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAuthTestServer(t, tc.cfgYAML)
			handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("REACHED"))
			}))

			// 内网来源：旧行为会放行，现在必须拒绝
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, reqFrom("192.168.1.50:51234", nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("凭据只配一半时必须拒绝, 实际状态码 %d（响应体 %s）",
					rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "REACHED") {
				t.Error("请求绕过了认证直达处理函数")
			}
		})
	}
}

// TestAuthMiddlewareAcceptsCompleteCredentials 完整凭据仍应正常工作。
func TestAuthMiddlewareAcceptsCompleteCredentials(t *testing.T) {
	srv := newAuthTestServer(t,
		"version: 1\nweb_ui:\n  port: \"7860\"\n  username: admin\n  password: s3cret\n")
	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := reqFrom("192.168.1.50:51234", nil)
	r.SetBasicAuth("admin", "s3cret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("正确凭据应放行, 实际 %d", rec.Code)
	}
}
