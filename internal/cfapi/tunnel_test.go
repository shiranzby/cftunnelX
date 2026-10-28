package cfapi

import (
	"errors"
	"testing"
)

// TestIsAuthError 权限类错误必须被识别出来，否则健康探测器会把
// "Token 缺 Zero Trust 读权限"误判成"隧道卡死"，进而无谓重启健康隧道。
func TestIsAuthError(t *testing.T) {
	authErrs := []string{
		"GET https://api.cloudflare.com/client/v4/accounts/x/cfd_tunnel/y/connections: 403 Forbidden",
		"401 Unauthorized",
		"Authentication error",
		"invalid token",
		"error code 10000: Authentication error",
		"\"success\":false,\"errors\":[{\"code\":10000}]",
	}
	for _, msg := range authErrs {
		if !IsAuthError(errors.New(msg)) {
			t.Errorf("应判定为权限错误但未识别: %s", msg)
		}
	}

	otherErrs := []string{
		"dial tcp 198.41.192.27:7844: i/o timeout",
		"context deadline exceeded",
		"no such host",
		"404 Not Found",
	}
	for _, msg := range otherErrs {
		if IsAuthError(errors.New(msg)) {
			t.Errorf("非权限错误被误判: %s", msg)
		}
	}

	if IsAuthError(nil) {
		t.Error("nil 不应被判为权限错误")
	}
}

// TestIsTunnelNotFound 隧道已被远端删除时不应触发重启。
func TestIsTunnelNotFound(t *testing.T) {
	if !IsTunnelNotFound(errors.New("404 Not Found")) {
		t.Error("404 应判定为隧道不存在")
	}
	if IsTunnelNotFound(errors.New("403 Forbidden")) {
		t.Error("403 不应判定为隧道不存在")
	}
	if IsTunnelNotFound(nil) {
		t.Error("nil 不应判定为隧道不存在")
	}
}

// TestErrorClassifiersAreDisjoint 两个分类器对同一输入不能同时为真，
// 否则探测分支的先后顺序会决定行为，容易埋雷。
func TestErrorClassifiersAreDisjoint(t *testing.T) {
	samples := []string{
		"401 Unauthorized",
		"403 Forbidden",
		"404 Not Found",
		"dial tcp: i/o timeout",
		"Authentication error",
	}
	for _, msg := range samples {
		err := errors.New(msg)
		if IsAuthError(err) && IsTunnelNotFound(err) {
			t.Errorf("同时被判为权限错误与不存在: %s", msg)
		}
	}
}
