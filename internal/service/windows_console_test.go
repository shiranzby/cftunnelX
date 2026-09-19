//go:build windows

package service

import "testing"

// TestParseServiceState 验证从 sc query 输出中解析状态。
// 旧实现用 strings.Contains(out, "RUNNING")，会把 STOP_PENDING 之类的
// 文本误判，也无法用于等待状态收敛。
func TestParseServiceState(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "运行中",
			out:  "SERVICE_NAME: Cloudflared\r\n        TYPE               : 10  WIN32_OWN_PROCESS\r\n        STATE              : 4  RUNNING\r\n",
			want: "RUNNING",
		},
		{
			name: "已停止",
			out:  "        STATE              : 1  STOPPED\r\n",
			want: "STOPPED",
		},
		{
			name: "停止中不应被误判为运行",
			out:  "        STATE              : 3  STOP_PENDING\r\n",
			want: "STOP_PENDING",
		},
		{
			name: "启动中",
			out:  "        STATE              : 2  START_PENDING\r\n",
			want: "START_PENDING",
		},
		{
			name: "空输出",
			out:  "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseServiceState(tc.out); got != tc.want {
				t.Errorf("parseServiceState() = %q, 期望 %q", got, tc.want)
			}
		})
	}
}

func TestQueryServiceNameMissing(t *testing.T) {
	// 一个几乎不可能存在的服务名，应返回 false 而不是 panic
	if queryServiceName("cftunnelx-definitely-not-installed-xyz") {
		t.Error("不存在的服务不应被判定为已安装")
	}
}
