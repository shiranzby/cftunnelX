package daemon

import (
	"os"
	"sort"
	"strconv"
	"strings"
)

// RunningInstance 描述一个由 cftunnelX 拉起的 cloudflared 实例。
type RunningInstance struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	Scope   string `json:"scope,omitempty"`    // tunnel:<id> / legacy / quick
	PidFile string `json:"pid_file,omitempty"` // 实际读到 PID 的文件
}

// AnyRunning 汇总所有可能存在的 cloudflared 实例。
//
// 为什么必须有这个函数：项目里并存三种 PID 文件命名——
//   - cloudflared-<隧道ID>.pid  （WebUI 启动隧道，多隧道形态）
//   - cloudflared.pid           （旧版单隧道兼容路径）
//   - quick.pid                 （免域名模式）
//
// 只检查其中一种会导致接口互相矛盾：面板 /api/status 走 RunningTunnels()
// 显示"运行中"，而诊断走 Running()（只读 cloudflared.pid）却显示"未运行"。
// 这类"同一个事实两个结论"的缺陷对使用者最具误导性，因此统一到本函数。
func AnyRunning() RunningInstance {
	// 1) 多隧道形态（WebUI 启动隧道的默认路径）
	if inst, ok := firstRunningTunnel(); ok {
		return inst
	}
	// 2) 旧版单隧道全局 PID 文件
	if pid := readPIDFile(pidFilePath()); pid > 0 && processRunning(pid) {
		return RunningInstance{Running: true, PID: pid, Scope: "legacy", PidFile: pidFilePath()}
	}
	// 3) 免域名模式
	if pid := readPIDFile(quickPIDPath()); pid > 0 && processRunning(pid) {
		return RunningInstance{Running: true, PID: pid, Scope: "quick", PidFile: quickPIDPath()}
	}
	return RunningInstance{}
}

// firstRunningTunnel 按隧道 ID 排序后返回第一个存活实例，
// 排序是为了让诊断输出稳定可复现（map 遍历顺序随机）。
func firstRunningTunnel() (RunningInstance, bool) {
	running := RunningTunnels()
	if len(running) == 0 {
		return RunningInstance{}, false
	}
	ids := make([]string, 0, len(running))
	for id := range running {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	id := ids[0]
	return RunningInstance{
		Running: true,
		PID:     running[id],
		Scope:   "tunnel:" + id,
		PidFile: tunnelPIDPath(id),
	}, true
}

// RunningScopeHint 把实例范围翻译成人类可读说明，用于诊断输出。
func (r RunningInstance) Hint() string {
	switch {
	case !r.Running:
		return ""
	case strings.HasPrefix(r.Scope, "tunnel:"):
		return "隧道 " + strings.TrimPrefix(r.Scope, "tunnel:") + " 正在运行（PID " + strconv.Itoa(r.PID) + "）"
	case r.Scope == "legacy":
		return "cloudflared 正在运行（旧版单隧道模式，PID " + strconv.Itoa(r.PID) + "）"
	case r.Scope == "quick":
		return "免域名模式正在运行（PID " + strconv.Itoa(r.PID) + "）"
	default:
		return "cloudflared 正在运行（PID " + strconv.Itoa(r.PID) + "）"
	}
}

func readPIDFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}
