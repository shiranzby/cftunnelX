package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// writeConfigFixture 把配置写入当前测试进程的配置目录。
// go test 时 os.Executable() 指向临时目录下的测试二进制，Dir() 随之落在临时目录。
func writeConfigFixture(body string) error {
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		return err
	}
	return os.WriteFile(config.Path(), []byte(body), 0600)
}

func reqFrom(remoteAddr string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// TestIsLocalRequestRejectsForgedForwardedHeader 是 P0 安全缺陷的回归测试。
//
// 旧实现只要看到 X-Forwarded-For: 127.0.0.1 就判定为本地请求并跳过认证，
// 外部攻击者因此可以绕过认证调用 /api/terminal/exec 执行任意命令。
func TestIsLocalRequestRejectsForgedForwardedHeader(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       bool
	}{
		{
			name:       "本机浏览器无代理头",
			remoteAddr: "127.0.0.1:51234",
			headers:    nil,
			want:       true,
		},
		{
			name:       "IPv6 回环",
			remoteAddr: "[::1]:51234",
			headers:    nil,
			want:       true,
		},
		{
			name:       "外部直连伪造 X-Forwarded-For 回环",
			remoteAddr: "203.0.113.9:51234",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			want:       false,
		},
		{
			name:       "外部直连伪造 CF-Connecting-IP 回环",
			remoteAddr: "203.0.113.9:51234",
			headers:    map[string]string{"CF-Connecting-IP": "127.0.0.1"},
			want:       false,
		},
		{
			name:       "外部直连伪造 X-Real-IP 回环",
			remoteAddr: "203.0.113.9:51234",
			headers:    map[string]string{"X-Real-IP": "127.0.0.1"},
			want:       false,
		},
		{
			name:       "外部直连无代理头",
			remoteAddr: "203.0.113.9:51234",
			headers:    nil,
			want:       false,
		},
		{
			name:       "隧道转发(回环连接 + CF 头)视为远程",
			remoteAddr: "127.0.0.1:51234",
			headers:    map[string]string{"CF-Connecting-IP": "203.0.113.9"},
			want:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLocalRequest(reqFrom(tc.remoteAddr, tc.headers)); got != tc.want {
				t.Errorf("isLocalRequest() = %v, 期望 %v", got, tc.want)
			}
		})
	}
}

func newAuthTestServer(t *testing.T, cfgYAML string) *Server {
	t.Helper()
	if err := writeConfigFixture(cfgYAML); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	return NewServer(cfg, "7860", "test")
}

// TestAuthMiddlewareFailsClosed 未配置认证时，远程请求必须被拒绝而不是放行。
func TestAuthMiddlewareFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		cfgYAML string
	}{
		{
			name:    "未开启远程访问",
			cfgYAML: "version: 1\nweb_ui:\n  port: \"7860\"\n  remote_enabled: false\n",
		},
		{
			name:    "开启远程访问但未配置账号密码",
			cfgYAML: "version: 1\nweb_ui:\n  port: \"7860\"\n  remote_enabled: true\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAuthTestServer(t, tc.cfgYAML)
			handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("REACHED"))
			}))

			r := reqFrom("203.0.113.9:51234", map[string]string{"X-Forwarded-For": "127.0.0.1"})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("状态码 = %d, 期望 401 (响应体: %s)", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "REACHED") {
				t.Fatal("远程请求绕过了认证直达业务处理函数")
			}
		})
	}
}

// TestAuthMiddlewareRequiresCredentials 配置了账号密码后，远程请求需通过 Basic Auth。
func TestAuthMiddlewareRequiresCredentials(t *testing.T) {
	srv := newAuthTestServer(t, "version: 1\nweb_ui:\n  port: \"7860\"\n  remote_enabled: true\n  username: admin\n  password: s3cret\n")
	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("REACHED"))
	}))

	t.Run("无凭据被拒绝", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, reqFrom("203.0.113.9:51234", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d, 期望 401", rec.Code)
		}
	})

	t.Run("错误凭据被拒绝", func(t *testing.T) {
		r := reqFrom("203.0.113.9:51234", nil)
		r.SetBasicAuth("admin", "wrong")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d, 期望 401", rec.Code)
		}
	})

	t.Run("正确凭据放行", func(t *testing.T) {
		r := reqFrom("203.0.113.9:51234", nil)
		r.SetBasicAuth("admin", "s3cret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d, 期望 200", rec.Code)
		}
	})

	t.Run("本地请求无需凭据", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, reqFrom("127.0.0.1:51234", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d, 期望 200", rec.Code)
		}
	})
}
