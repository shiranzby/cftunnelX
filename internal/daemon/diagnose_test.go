package daemon

import (
	"errors"
	"strings"
	"testing"
)

// TestDescribeEdgeStatus 锁定 Cloudflare 边缘状态码到具体原因的映射。
//
// 这是"链路不可达"排查的关键：530 表示隧道未连接，502 表示源站不可达，
// 404 表示路由缺失——三者处理方式完全不同，不能都显示一句"不可达"。
func TestDescribeEdgeStatus(t *testing.T) {
	cases := []struct {
		code     int
		mustHave string
	}{
		{530, "隧道未连接"},
		{502, "无法访问源站"},
		{404, "没有匹配的路由"},
		{403, "Access"},
		{522, "回源异常"},
		{418, "418"},
	}
	for _, tc := range cases {
		got := describeEdgeStatus(tc.code)
		if !strings.Contains(got, tc.mustHave) {
			t.Errorf("describeEdgeStatus(%d) = %q, 应包含 %q", tc.code, got, tc.mustHave)
		}
	}
}

// TestClassifyHTTPSError 网络层错误必须被翻译成可操作的原因。
func TestClassifyHTTPSError(t *testing.T) {
	cases := []struct {
		err      string
		mustHave string
	}{
		{"x509: certificate signed by unknown authority", "根证书"},
		{"tls: failed to verify certificate", "根证书"},
		{"dial tcp: lookup a.example.com: no such host", "DNS"},
		{"context deadline exceeded (Client.Timeout exceeded)", "超时"},
		{"dial tcp 1.2.3.4:443: connect: connection refused", "拒绝"},
		{"dial tcp: network is unreachable", "网络不可达"},
		{"unexpected EOF", "中断"},
		{"something totally unknown", "HTTPS 请求失败"},
	}
	for _, tc := range cases {
		got := classifyHTTPSError(errors.New(tc.err))
		if !strings.Contains(got, tc.mustHave) {
			t.Errorf("classifyHTTPSError(%q) = %q, 应包含 %q", tc.err, got, tc.mustHave)
		}
	}
}

// TestCheckCACertificates 根证书检查不应 panic，且 Windows 恒为可用。
func TestCheckCACertificates(t *testing.T) {
	ok, path, hint := CheckCACertificates()
	if ok {
		if path == "" {
			t.Error("判定可用时应同时返回证书路径")
		}
		if hint != "" {
			t.Errorf("判定可用时不应有提示, 实际 %q", hint)
		}
		return
	}
	// 判定缺失时必须给出安装指引，否则用户无从下手
	for _, want := range []string{"ca-certificates", "ca-bundle"} {
		if !strings.Contains(hint, want) {
			t.Errorf("缺失根证书时的提示应包含 %q:\n%s", want, hint)
		}
	}
}

// TestExtractPortServiceFormats 端口提取要能处理常见的 Service 写法。
func TestExtractPortServiceFormats(t *testing.T) {
	cases := map[string]string{
		"http://localhost:3000":  "3000",
		"http://127.0.0.1:8080":  "8080",
		"https://localhost:8443": "8443",
		"http://localhost":       "",
		"localhost:3000":         "3000",
	}
	for service, want := range cases {
		if got := extractPort(service); got != want {
			t.Errorf("extractPort(%q) = %q, 期望 %q", service, got, want)
		}
	}
}
