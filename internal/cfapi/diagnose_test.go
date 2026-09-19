package cfapi

import (
	"errors"
	"strings"
	"testing"
)

// TestDescribeTokenFailure 凭据校验失败必须区分"Token 无效"与"填成了 Global API Key"。
//
// 这是"设置页刚填完 API 就说不可达"的核心区分解释：旧提示一律说
// "Token 无效或已过期"，而真实原因可能是认证方式用错。
func TestDescribeTokenFailure(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		code     int
		msg      string
		mustHave []string
	}{
		{
			name:     "Token 无效",
			status:   401,
			code:     1000,
			msg:      "Invalid API Token",
			mustHave: []string{"API Token 无效", "Global API Key"},
		},
		{
			name:     "凭据被拒绝（非 1000 码）",
			status:   403,
			code:     9109,
			msg:      "Unauthorized",
			mustHave: []string{"凭据被拒绝", "请使用 API Token"},
		},
		{
			name:     "服务端错误",
			status:   503,
			code:     0,
			msg:      "Service unavailable",
			mustHave: []string{"请稍后重试"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := describeTokenFailure(tc.status, tc.code, tc.msg)
			for _, want := range tc.mustHave {
				if !strings.Contains(got, want) {
					t.Errorf("describeTokenFailure(%d,%d,%q) = %q, 应包含 %q",
						tc.status, tc.code, tc.msg, got, want)
				}
			}
		})
	}
}

// TestDescribeNetErr 网络层错误要能识别证书缺失这一 WSL/精简镜像的高频原因。
func TestDescribeNetErr(t *testing.T) {
	cases := []struct {
		err      string
		mustHave string
	}{
		{"Get \"https://api.cloudflare.com\": x509: certificate signed by unknown authority", "根证书"},
		{"dial tcp: lookup api.cloudflare.com: no such host", "DNS 解析失败"},
		{"context deadline exceeded", "超时"},
		{"dial tcp: network is unreachable", "网络不可达"},
	}
	for _, tc := range cases {
		if got := describeNetErr(errors.New(tc.err)); !strings.Contains(got, tc.mustHave) {
			t.Errorf("describeNetErr(%q) = %q, 应包含 %q", tc.err, got, tc.mustHave)
		}
	}
}

// TestCredentialDiagnosisReachable Token 有效即视为凭据可用，
// 即使缺少 Zone Read 权限（该权限只影响界面上的域名列表）。
func TestCredentialDiagnosisReachable(t *testing.T) {
	valid := CredentialDiagnosis{TokenValid: true, ZonesOK: false}
	if !valid.Reachable() {
		t.Error("Token 有效时 Reachable() 应为 true（缺 Zone 权限不应判定为不可用）")
	}
	invalid := CredentialDiagnosis{TokenValid: false}
	if invalid.Reachable() {
		t.Error("Token 无效时 Reachable() 应为 false")
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("第一行\n第二行"); got != "第一行" {
		t.Errorf("firstLine 应只取首行, 实际 %q", got)
	}
	long := strings.Repeat("a", 300)
	got := firstLine(long)
	if len(got) > 210 || !strings.HasSuffix(got, "...") {
		t.Errorf("超长内容应被截断, 实际长度 %d", len(got))
	}
}
