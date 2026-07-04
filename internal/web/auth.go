package web

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// isLocalRequest 判断请求是否来自本地
func isLocalRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "127.0.0.1", "::1", "localhost", "":
		return true
	}
	return false
}

// noAuthPaths 不需要认证的 API 路径（即使远程访问也不拦截）
// 这些是"引导性"端点：配置认证、查看版本、设置主题等
var noAuthPaths = map[string]bool{
	"/api/webpanel":  true,
	"/api/config":    true,
	"/api/theme":     true,
	"/api/version":   true,
	"/api/language":  true,
	"/api/port":      true,
}

// isNoAuthPath 检查路径是否在免认证白名单中
func isNoAuthPath(path string) bool {
	// 精确匹配
	if noAuthPaths[path] {
		return true
	}
	// 前缀匹配（如 /api/config/test）
	for p := range noAuthPaths {
		if strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// authMiddleware 当启用远程访问时，对非本地请求进行 Basic Auth
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 本地请求始终放行
		if isLocalRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		// 免认证路径放行（配置/面板/版本等）
		if isNoAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// 静态资源放行
		if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		// 检查是否启用了远程访问
		cfg, err := config.Load()
		if err != nil || !cfg.WebUI.RemoteEnabled {
			next.ServeHTTP(w, r)
			return
		}
		// 启用远程访问且非本地 → 需要 Basic Auth
		// 留空则无认证（用户主动关闭认证）
		if cfg.WebUI.Username == "" || cfg.WebUI.Password == "" {
			// 账号或密码为空 → 不认证，直接放行
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(user), []byte(cfg.WebUI.Username)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.WebUI.Password)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="cftunnel"`)
			http.Error(w, "认证失败", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}
