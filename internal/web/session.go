package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// 会话 Cookie 机制
//
// 为什么需要这一层：去掉 WWW-Authenticate 后，浏览器不再自动保存/附带 Basic 凭据。
// 仅靠前端把凭据存进 sessionStorage 并不够 —— 浏览器发起的导航请求
// （location.href、地址栏回车、刷新、书签打开）不会带任何自定义头，
// 于是登录成功后跳转主页又被判定为未认证，形成死循环。
//
// 因此改用服务端签发的会话 Cookie：登录一次、长期免登录，
// 导航请求与页面内 fetch 都能通过认证。
//
// 设计要点：
//   - 令牌落盘持久化，服务重启后免登录继续有效（满足"一台机器认证一次"）
//   - 绑密码指纹，改密码后旧令牌立即失效
//   - HttpOnly 防 XSS 读取；SameSite=Lax 允许从外部链接进入时携带
//   - 不设 Secure：面板常通过 http:// 局域网 IP 访问

const (
	sessionCookieName = "cft_session"
	// 会话有效期 365 天。对自托管面板而言，浏览器本地保存凭据本就是常态，
	// 过短的有效期只会迫使用户反复登录，得不偿失。
	// 真正的失效触发点是改密码或关闭认证，而不是时间。
	sessionTTL = 365 * 24 * time.Hour
)

// sessionEntry 是落盘的单条会话。
type sessionEntry struct {
	Username  string `json:"username"`
	CredFP    string `json:"cred_fp"`
	ExpiresAt int64  `json:"expires_at"`
}

var (
	// sessionMu 保护会话文件的读写。并发请求会同时命中，
	// 因此 load/save 必须成对加锁。
	sessionMu sync.Mutex
)

// credFingerprint 由账号+密码派生，用于让改密码立即作废所有旧会话。
// 用 SHA-256 而非明文存储密码摘要，避免离线爆破。
func credFingerprint(username, password string) string {
	sum := sha256.Sum256([]byte(username + "\x00" + password))
	return hex.EncodeToString(sum[:])
}

// sessionStorePath 返回会话持久化文件路径。
// 每次都重新解析 config.Dir()，不缓存 —— 测试会反复切换临时配置目录，
// 缓存路径会导致后续用例写到已删除的目录。
func sessionStorePath() string {
	return filepath.Join(config.Dir(), "web_sessions.json")
}

// loadSessions 读取持久化会话；文件不存在或损坏时返回空集合。
// 损坏不应导致面板无法启动。
func loadSessions() map[string]sessionEntry {
	out := make(map[string]sessionEntry)
	b, err := os.ReadFile(sessionStorePath())
	if err != nil {
		return out
	}
	// 容忍部分损坏：逐条解析，坏行跳过
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := parseEntryLine(line)
		if ok {
			out[k] = v
		}
	}
	return out
}

// parseEntryLine 解析 "hash<TAB>json" 形式的单行。
func parseEntryLine(line string) (string, sessionEntry, bool) {
	tab := strings.IndexByte(line, '\t')
	if tab <= 0 {
		return "", sessionEntry{}, false
	}
	key := line[:tab]
	rest := line[tab+1:]
	var e sessionEntry
	// 轻量解析：只取三个字符串/数字字段，避免为一个文件引入 JSON 依赖解析开销
	if !parseEntryFields(rest, &e) {
		return "", sessionEntry{}, false
	}
	return key, e, true
}

// parseEntryFields 解析形如 {"username":"x","cred_fp":"y","expires_at":123}
func parseEntryFields(s string, e *sessionEntry) bool {
	fp := strings.Index(s, `"username":"`)
	if fp < 0 {
		return false
	}
	fp += len(`"username":"`)
	fe := strings.IndexByte(s[fp:], '"')
	if fe < 0 {
		return false
	}
	e.Username = s[fp : fp+fe]

	cp := strings.Index(s, `"cred_fp":"`)
	if cp < 0 {
		return false
	}
	cp += len(`"cred_fp":"`)
	ce := strings.IndexByte(s[cp:], '"')
	if ce < 0 {
		return false
	}
	e.CredFP = s[cp : cp+ce]

	ep := strings.Index(s, `"expires_at":`)
	if ep < 0 {
		return false
	}
	ep += len(`"expires_at":`)
	ee := ep
	for ee < len(s) && s[ee] >= '0' && s[ee] <= '9' {
		ee++
	}
	if ee == ep {
		return false
	}
	n := int64(0)
	for i := ep; i < ee; i++ {
		n = n*10 + int64(s[i]-'0')
	}
	e.ExpiresAt = n
	return true
}

// saveSessions 原子写入：先写临时文件再改名，避免进程中断留下半截文件。
func saveSessions(m map[string]sessionEntry) {
	var sb strings.Builder
	now := time.Now().Unix()
	for k, v := range m {
		if v.ExpiresAt < now {
			continue // 过期的不再落盘
		}
		sb.WriteString(k)
		sb.WriteByte('\t')
		sb.WriteString(`{"username":"` + v.Username + `","cred_fp":"` + v.CredFP + `","expires_at":` + itoa(v.ExpiresAt) + "}\n")
	}
	p := sessionStorePath()
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

// itoa 避免为一个转换引入 strconv 之外的依赖差异。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// newSessionToken 生成不可预测的随机会话令牌。
func newSessionToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 随机源失败时退化为时间派生值；这种情况极罕见
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// sessionHash 对令牌做摘要后再作为存储 key，避免令牌原文落盘。
func sessionHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// createSession 签发会话令牌并持久化。
func createSession(username, password string) string {
	token := newSessionToken()
	sessionMu.Lock()
	defer sessionMu.Unlock()

	m := loadSessions()
	// 顺手清理过期会话
	nowSec := time.Now().Unix()
	for k, v := range m {
		if v.ExpiresAt < nowSec {
			delete(m, k)
		}
	}
	m[sessionHash(token)] = sessionEntry{
		Username:  username,
		CredFP:    credFingerprint(username, password),
		ExpiresAt: time.Now().Add(sessionTTL).Unix(),
	}
	saveSessions(m)
	return token
}

// validSession 校验令牌。用户名、密码指纹与过期时间任一不符即失效，
// 因此改密码或改用户名都会强制重新登录。
func validSession(token, username, password string) bool {
	if token == "" || username == "" {
		return false
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()

	key := sessionHash(token)
	m := loadSessions()
	e, ok := m[key]
	if !ok {
		return false
	}
	if e.ExpiresAt < time.Now().Unix() {
		delete(m, key)
		saveSessions(m)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(e.Username), []byte(username)) != 1 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(e.CredFP), []byte(credFingerprint(username, password))) != 1 {
		return false
	}
	return true
}

// invalidateSessionsFor 在改密码 / 关闭认证时清空所有会话。
func invalidateSessionsFor(username string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	m := loadSessions()
	changed := false
	for k, v := range m {
		if username == "" || v.Username == username {
			delete(m, k)
			changed = true
		}
	}
	// 无变化时不落盘：该函数在未认证分支会被每个请求触发，
	// 空写会造成无谓的磁盘 IO。
	if changed {
		saveSessions(m)
	}
}

// setSessionCookie 下发会话 Cookie。
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// clearSessionCookie 清除会话（登出、改密码或关闭认证时）。
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// requestHasValidSession 判断请求是否带有效会话。
func requestHasValidSession(r *http.Request, username, password string) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return validSession(c.Value, username, password)
}
