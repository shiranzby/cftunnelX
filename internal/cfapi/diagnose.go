package cfapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CloudflareAPIBase Cloudflare API v4 根地址
const CloudflareAPIBase = "https://api.cloudflare.com/client/v4"

// apiEnvelope Cloudflare 统一响应信封
type apiEnvelope struct {
	Success  bool            `json:"success"`
	Errors   []apiErrorItem  `json:"errors"`
	Messages []interface{}   `json:"messages"`
	Result   json.RawMessage `json:"result"`
}

type apiErrorItem struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e apiEnvelope) firstError() (int, string) {
	if len(e.Errors) == 0 {
		return 0, ""
	}
	return e.Errors[0].Code, e.Errors[0].Message
}

// CredentialDiagnosis 凭据与权限的分层诊断结果。
//
// 为什么要分层：旧实现只用 Zones.List 一个接口来判断"API 是否可达"，
// 而该接口需要 Zone:Zone Read 权限。于是"Token 本身完全有效、只是没勾这个
// 权限"的情况会被笼统显示成"Cloudflare API 不可达 / Token 无效或已过期"，
// 用户无从下手。分层后可以准确指出是哪一层出了问题。
type CredentialDiagnosis struct {
	// 第一层：Token 本身是否有效（/user/tokens/verify，不需要任何额外权限）
	TokenValid   bool   `json:"token_valid"`
	TokenStatus  int    `json:"token_status,omitempty"`
	TokenErrCode int    `json:"token_err_code,omitempty"`
	TokenMessage string `json:"token_message,omitempty"`

	// 第二层：Account ID 是否存在且可访问
	AccountChecked bool   `json:"account_checked"`
	AccountOK      bool   `json:"account_ok"`
	AccountStatus  int    `json:"account_status,omitempty"`
	AccountMessage string `json:"account_message,omitempty"`

	// 第三层：能否列出域名（需要 Zone:Zone Read）
	ZonesChecked bool   `json:"zones_checked"`
	ZonesOK      bool   `json:"zones_ok"`
	ZoneCount    int    `json:"zone_count"`
	ZonesMessage string `json:"zones_message,omitempty"`

	LatencyMS int64  `json:"latency_ms,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// Reachable 供界面直接判断"凭据是否可用"：
// 只要 Token 本身有效就算凭据可用，Zone 权限缺失不影响隧道与 DNS 操作。
func (d CredentialDiagnosis) Reachable() bool {
	return d.TokenValid
}

// rawGet 发起一次带 Bearer 认证的 GET，返回状态码与解析后的信封。
func (c *Client) rawGet(ctx context.Context, path string) (int, apiEnvelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CloudflareAPIBase+path, nil)
	if err != nil {
		return 0, apiEnvelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.apiToken))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, apiEnvelope{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, apiEnvelope{}, err
	}
	var env apiEnvelope
	if len(body) > 0 {
		_ = json.Unmarshal(body, &env)
	}
	return resp.StatusCode, env, nil
}

// DiagnoseCredentials 分层校验凭据：Token 有效性 → Account ID → Zone 读取权限。
//
// 任何一层失败都会给出该层专属的、可操作的原因，而不是统一的"不可达"。
func (c *Client) DiagnoseCredentials(ctx context.Context) CredentialDiagnosis {
	var d CredentialDiagnosis
	start := time.Now()

	// ---- 第一层：Token 是否有效 ----
	status, env, err := c.rawGet(ctx, "/user/tokens/verify")
	d.TokenStatus = status
	d.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		d.TokenMessage = "无法连接 Cloudflare API：" + describeNetErr(err)
		d.Hint = "请检查出方向网络、系统根证书与防火墙（Debian/Ubuntu 可执行 apt-get install -y ca-certificates）"
		return d
	}
	code, msg := env.firstError()
	if status == http.StatusOK && env.Success {
		d.TokenValid = true
	} else {
		d.TokenErrCode = code
		d.TokenMessage = fmt.Sprintf("HTTP %d", status)
		if msg != "" {
			d.TokenMessage += " / " + msg
		}
		d.Hint = describeTokenFailure(status, code, msg)
		return d
	}

	// ---- 第二层：Account ID ----
	if strings.TrimSpace(c.accountID) == "" {
		d.AccountMessage = "未填写 Account ID"
		d.Hint = "请在「基本配置」中填写 Account ID（Cloudflare 控制台右侧概览处可复制）"
		return d
	}
	d.AccountChecked = true
	if status, env, err := c.rawGet(ctx, "/accounts/"+strings.TrimSpace(c.accountID)); err != nil {
		d.AccountMessage = "无法连接 Cloudflare API：" + describeNetErr(err)
	} else if status == http.StatusOK && env.Success {
		d.AccountOK = true
	} else {
		c2, m2 := env.firstError()
		d.AccountStatus = status
		d.AccountMessage = fmt.Sprintf("HTTP %d", status)
		if m2 != "" {
			d.AccountMessage += " / " + m2
		}
		switch status {
		case http.StatusNotFound:
			d.Hint = "Account ID 不正确：Cloudflare 找不到该账号。请核对控制台概览中的 Account ID"
		case http.StatusForbidden, http.StatusUnauthorized:
			d.Hint = "Token 有效，但缺少 Account:Account Read 权限，或该 Token 无权访问此账号。" +
				"若账号正确，可在 Token 权限里补上 Account Read（不改权限也能正常用隧道，此检查仅为提示）"
		default:
			d.Hint = fmt.Sprintf("Account ID 校验返回 HTTP %d（错误码 %d）", status, c2)
		}
	}

	// ---- 第三层：Zone 读取权限 ----
	d.ZonesChecked = true
	zones, zErr := c.ListZones(ctx)
	if zErr == nil {
		d.ZonesOK = true
		d.ZoneCount = len(zones)
		if d.Hint == "" {
			d.Hint = fmt.Sprintf("凭据有效，账号可访问，可管理 %d 个域名", d.ZoneCount)
		}
		return d
	}
	d.ZonesMessage = zErr.Error()
	lower := strings.ToLower(d.ZonesMessage)
	switch {
	case strings.Contains(lower, "403") || strings.Contains(lower, "9109") ||
		strings.Contains(lower, "unauthorized") || strings.Contains(lower, "authentication"):
		d.Hint = "Token 有效，但缺少 Zone:Zone Read 权限，因此无法列出域名。" +
			"该权限只在「选择域名/列出域名」时需要，不影响创建隧道与写 DNS 记录；" +
			"如需在界面下拉选择域名，请在 Token 权限中补上 Zone:Zone Read"
	case strings.Contains(lower, "certificate") || strings.Contains(lower, "x509"):
		d.Hint = "TLS 证书校验失败：系统根证书缺失。" +
			"Debian/Ubuntu: apt-get install -y ca-certificates；OpenWrt: opkg install ca-bundle"
	default:
		d.Hint = "Token 有效，但读取域名列表失败：" + firstLine(d.ZonesMessage) +
			"（不影响隧道与 DNS 操作，仅影响界面上的域名列表）"
	}
	return d
}

// describeTokenFailure 把 /user/tokens/verify 的失败翻译成可操作原因。
func describeTokenFailure(status, code int, msg string) string {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if code == 1000 || strings.Contains(strings.ToLower(msg), "invalid api token") {
			return "API Token 无效。请到 dash.cloudflare.com/profile/api-tokens 重新创建，" +
				"注意：这里要填的是 **API Token**，不是 Global API Key（两者认证方式不同，Global Key 无法通过 Bearer 认证）。" +
				"建议模板选「编辑 Cloudflare Tunnel」+「编辑区域 DNS」"
		}
		return fmt.Sprintf("凭据被拒绝（HTTP %d，错误码 %d，%s）："+
			"可能是 Token 已过期/已撤销，或填入了 Global API Key。%s",
			status, code, msg, "请使用 API Token")
	}
	if status >= 500 {
		return fmt.Sprintf("Cloudflare 服务端返回 HTTP %d，请稍后重试", status)
	}
	return fmt.Sprintf("凭据校验失败（HTTP %d，错误码 %d，%s）", status, code, msg)
}

// describeNetErr 把网络层错误翻译成可操作原因。
func describeNetErr(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "certificate") || strings.Contains(s, "x509"):
		return "TLS 证书校验失败，系统根证书缺失"
	case strings.Contains(s, "no such host"):
		return "DNS 解析失败（api.cloudflare.com 无法解析）"
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded"):
		return "连接超时"
	case strings.Contains(s, "network is unreachable") || strings.Contains(s, "no route to host"):
		return "网络不可达"
	default:
		return firstLine(err.Error())
	}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
