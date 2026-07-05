package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
)

const authCookieName = "cftunnelx_session"

// isLocalRequest 判断请求是否来自本机。Cloudflare Tunnel 会从本地回连，
// 因此存在代理头时优先使用真实客户端 IP。
func isLocalRequest(r *http.Request) bool {
	host := clientHost(r)
	switch host {
	case "127.0.0.1", "::1", "localhost", "":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func clientHost(r *http.Request) string {
	for _, name := range []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"} {
		v := strings.TrimSpace(r.Header.Get(name))
		if v == "" {
			continue
		}
		if name == "X-Forwarded-For" {
			v = strings.TrimSpace(strings.Split(v, ",")[0])
		}
		if v != "" {
			return strings.Trim(v, "[]")
		}
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return strings.Trim(host, "[]")
}

var noAuthPaths = map[string]bool{
	"/login":       true,
	"/api/login":   true,
	"/api/version": true,
}

func isNoAuthPath(path string) bool {
	if noAuthPaths[path] {
		return true
	}
	return path == "/assets/logo.png"
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.Load()
		if !authRequired(cfg, err) {
			next.ServeHTTP(w, r)
			return
		}
		if isLocalRequest(r) || isNoAuthPath(r.URL.Path) || s.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if !authRequired(cfg, err) || isLocalRequest(r) || s.validSession(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginTemplate.Execute(w, map[string]string{"Version": s.version})
}

func authRequired(cfg *config.Config, err error) bool {
	return err == nil &&
		cfg != nil &&
		cfg.WebUI.RemoteEnabled &&
		strings.TrimSpace(cfg.WebUI.Username) != "" &&
		strings.TrimSpace(cfg.WebUI.Password) != ""
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Username), []byte(cfg.WebUI.Username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(body.Password), []byte(cfg.WebUI.Password)) != 1 {
		writeError(w, http.StatusUnauthorized, "账号或密码错误")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    s.signSession(time.Now().Add(24 * time.Hour)),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
	writeOK(w, map[string]string{"status": "ok"})
}

func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return false
	}
	exp, err := time.Parse(time.RFC3339, parts[0])
	if err != nil || time.Now().After(exp) {
		return false
	}
	return hmac.Equal([]byte(parts[1]), []byte(s.sessionSig(parts[0])))
}

func (s *Server) signSession(exp time.Time) string {
	payload := exp.UTC().Format(time.RFC3339)
	return payload + "." + s.sessionSig(payload)
}

func (s *Server) sessionSig(payload string) string {
	mac := hmac.New(sha256.New, []byte(s.authKey))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="zh-CN" data-theme="light">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<title>cftunnelX 登录</title>
<style>
*{box-sizing:border-box}body{margin:0;min-height:100vh;font:14px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Arial,sans-serif;background:#f7f9fc;color:#172033;display:grid;place-items:center;padding:24px}.card{width:min(420px,100%);background:#fff;border:1px solid #e7edf5;border-radius:8px;box-shadow:0 18px 44px rgba(15,23,42,.08);padding:24px}.brand{display:flex;align-items:center;gap:12px;margin-bottom:18px}.brand img{width:48px;height:48px;object-fit:contain;border-radius:8px}.brand strong{font-size:24px;font-weight:950}.brand span{color:#2563eb}p{margin:0 0 18px;color:#6b7688}.fi{width:100%;height:40px;border:1px solid #e7edf5;border-radius:8px;background:#f8fafc;color:#172033;padding:0 12px;outline:0;margin-bottom:10px}.fi:focus{border-color:#2563eb;box-shadow:0 0 0 3px rgba(37,99,235,.12)}.btn{width:100%;height:40px;border:0;border-radius:8px;background:#2563eb;color:#fff;font-weight:900;cursor:pointer}.err{display:none;margin-top:12px;padding:10px 12px;border-radius:8px;background:rgba(220,38,38,.1);color:#dc2626;border:1px solid rgba(220,38,38,.18)}
</style>
</head>
<body>
<div class="card">
  <div class="brand"><img src="/assets/logo.png" alt=""><div><strong>cftunnel<span>X</span></strong><p>连接内网 · 安全穿透</p></div></div>
  <input id="u" class="fi" autocomplete="username" placeholder="管理账号">
  <input id="p" class="fi" type="password" autocomplete="current-password" placeholder="管理密码">
  <button class="btn" onclick="login()">登录</button>
  <div id="e" class="err"></div>
</div>
<script>
function login(){fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username:u.value,password:p.value})}).then(function(r){return r.json().then(function(d){if(!r.ok)throw Error(d.error||'登录失败');location.href='/'})}).catch(function(err){e.style.display='block';e.textContent=err.message})}
document.addEventListener('keydown',function(ev){if(ev.key==='Enter')login()})
</script>
</body>
</html>`))
