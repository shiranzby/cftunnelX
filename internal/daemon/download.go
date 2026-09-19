package daemon

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
)

// CloudflaredPath 返回 cloudflared 二进制路径
func CloudflaredPath() string {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name = "cloudflared.exe"
	}
	return filepath.Join(config.BinDir(), name)
}

// fileUsable 判断文件是否存在且可用（非目录、非空）。
// 用于识别上次中断留下的 0 字节残留，避免误认为"已安装"。
func fileUsable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// EmbeddedCloudflaredSize 返回内嵌 cloudflared 的大小（字节），0 表示未内嵌。
func EmbeddedCloudflaredSize() int {
	return len(cloudflaredAsset)
}

// extractEmbeddedCloudflared 把内嵌的 cloudflared 解压写入 dest。
//
// 先写同目录临时文件再原子改名，避免中断留下半截文件被 fileUsable 误判可用
// （解压失败时长度可能非 0）。
func extractEmbeddedCloudflared(dest string) error {
	if len(cloudflaredAsset) == 0 {
		return fmt.Errorf("本二进制未内嵌 cloudflared")
	}
	gr, err := gzip.NewReader(bytes.NewReader(cloudflaredAsset))
	if err != nil {
		return fmt.Errorf("内嵌 cloudflared 解压失败: %w", err)
	}
	defer gr.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, gr); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("写入 cloudflared 失败: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmp, 0755); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// EnsureCloudflared 确保 cloudflared 已安装，不存在时依次尝试：
// 随包附带 → 系统 PATH → 内嵌资源 → 网络下载。
func EnsureCloudflared() (string, error) {
	path := CloudflaredPath()
	if fileUsable(path) {
		return path, nil
	}
	if p, ok := bundledCloudflaredPath(); ok {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", err
		}
		if err := copyFile(p, path); err != nil {
			return "", err
		}
		if runtime.GOOS != "windows" {
			_ = os.Chmod(path, 0755)
		}
		return path, nil
	}
	// 尝试系统 PATH（例如 OpenWrt 用 opkg 安装的 cloudflared）
	if p, err := exec.LookPath("cloudflared"); err == nil && fileUsable(p) {
		return p, nil
	}
	if err := config.Ensure(); err != nil {
		return "", err
	}
	// 内嵌资源优先于网络下载：离线可用，也避免镜像源不可达时卡住
	if len(cloudflaredAsset) > 0 {
		fmt.Printf("正在从内嵌资源释放 cloudflared（约 %d MB，无需联网）...\n", len(cloudflaredAsset)>>20)
		if err := extractEmbeddedCloudflared(path); err != nil {
			return "", err
		}
		fmt.Printf("cloudflared 已释放到 %s\n", path)
		return path, nil
	}
	return path, download(path)
}

func bundledCloudflaredPath() (string, bool) {
	return BundledCloudflaredPath()
}

// BundledCloudflaredPath 查找随包附带或用户手工放置的 cloudflared。
// 覆盖离线部署场景：把二进制放在可执行文件同级、同级 bin/、config/bin/ 或依赖目录。
func BundledCloudflaredPath() (string, bool) {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name = "cloudflared.exe"
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, name),
			filepath.Join(dir, "bin", name),
			filepath.Join(dir, "config", "bin", name),
		)
	}
	candidates = append(candidates, filepath.Join(config.BinDir(), name))

	for _, p := range candidates {
		if fileUsable(p) {
			return p, true
		}
	}
	return "", false
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".copy"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// GitHub 镜像源列表（按优先级排序，最后一个是原始地址兜底）
var mirrors = []string{
	"https://ghfast.top/",
	"https://gh-proxy.com/",
	"https://ghproxy.cn/",
	"", // 原始 GitHub 地址
}

var cloudflaredDownloadMu sync.Mutex

// downloadURLs 返回待尝试的下载地址列表。
//
// 优先使用 CFTUNNEL_CLOUDFLARED_URL 显式指定的地址（离线部署、自建镜像，
// 以及在无官方构建的架构上自行提供的二进制）；否则按 镜像 × 资产名 组合。
func downloadURLs() ([]string, error) {
	if override := strings.TrimSpace(os.Getenv("CFTUNNEL_CLOUDFLARED_URL")); override != "" {
		fmt.Printf("使用 CFTUNNEL_CLOUDFLARED_URL 指定的下载地址\n")
		return []string{override}, nil
	}
	assets := cloudflaredAssets()
	if len(assets) == 0 {
		return nil, unsupportedPlatformError()
	}
	const origin = "https://github.com/cloudflare/cloudflared/releases/latest/download/"
	urls := make([]string, 0, len(mirrors)*len(assets))
	for _, mirror := range mirrors {
		for _, asset := range assets {
			urls = append(urls, mirror+origin+asset)
		}
	}
	return urls, nil
}

func download(dest string) error {
	cloudflaredDownloadMu.Lock()
	defer cloudflaredDownloadMu.Unlock()
	if fileUsable(dest) {
		return nil
	}

	urls, err := downloadURLs()
	if err != nil {
		return err
	}
	fmt.Println("正在下载 cloudflared...")

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	client := &http.Client{Timeout: 120 * time.Second}
	var lastErr error
	for _, url := range urls {
		label := url
		if idx := strings.Index(label, "://"); idx >= 0 {
			label = label[idx+3:]
		}
		if len(label) > 90 {
			label = label[:90] + "..."
		}
		fmt.Printf("尝试下载: %s\n", label)

		resp, err := client.Get(url)
		if err != nil {
			fmt.Printf("  连接失败: %v\n", err)
			lastErr = err
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			fmt.Printf("  HTTP %d\n", resp.StatusCode)
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, label)
			continue
		}

		// 以 URL 末段作为文件名提示，用于判断是否需要解压 .tgz
		nameHint := url
		if idx := strings.LastIndex(nameHint, "/"); idx >= 0 {
			nameHint = nameHint[idx+1:]
		}
		err = saveCloudflared(resp.Body, dest, nameHint)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		fmt.Printf("cloudflared 已下载到 %s\n", dest)
		return nil
	}
	return fmt.Errorf("所有下载源均失败，最后错误: %w", lastErr)
}

// saveCloudflared 将下载内容保存到目标路径
func saveCloudflared(r io.Reader, dest, filename string) error {
	tmp := dest + ".download"
	_ = os.Remove(tmp)
	defer os.Remove(tmp)
	if strings.HasSuffix(filename, ".tgz") {
		if err := extractTgz(r, tmp); err != nil {
			return err
		}
		return os.Rename(tmp, dest)
	}
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		os.Chmod(tmp, 0755)
	}
	return os.Rename(tmp, dest)
}

func extractTgz(r io.Reader, dest string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("tgz 中未找到 cloudflared")
		}
		if err != nil {
			return fmt.Errorf("解压失败: %w", err)
		}
		if filepath.Base(hdr.Name) == "cloudflared" {
			f, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
			os.Chmod(dest, 0755)
			return nil
		}
	}
}

// cloudflaredAssets 返回当前平台可用的 cloudflared 发行资产名，按优先级排列。
//
// 资产名以 cloudflare/cloudflared 实际发布内容为准（2026.9.1 核对）：
//
//	darwin-amd64.tgz / darwin-arm64.tgz
//	linux-amd64 / linux-arm64 / linux-arm / linux-armhf / linux-386
//	windows-amd64.exe / windows-386.exe
//
// 注意：cloudflare 未提供 mips / mipsle / riscv64 / loong64 的构建，
// 因此 OpenWrt 的 mipsel_24kc、mips_24kc、riscv64 目标无法运行 Cloud Tunnel，
// 应改用 relay（frp）模式——frp 覆盖上述全部架构。
func cloudflaredAssets() []string {
	return cloudflaredAssetsFor(runtime.GOOS, runtime.GOARCH)
}

// cloudflaredAssetsFor 按 GOOS/GOARCH 返回资产名，便于对全部架构做单元测试。
func cloudflaredAssetsFor(goos, goarch string) []string {
	switch goos + "/" + goarch {
	case "darwin/arm64":
		return []string{"cloudflared-darwin-arm64.tgz"}
	case "darwin/amd64":
		return []string{"cloudflared-darwin-amd64.tgz"}
	case "linux/amd64":
		return []string{"cloudflared-linux-amd64"}
	case "linux/arm64":
		return []string{"cloudflared-linux-arm64"}
	case "linux/arm":
		// armhf 优先：OpenWrt 的 arm_cortex-a7 属于硬件浮点 ABI
		return []string{"cloudflared-linux-armhf", "cloudflared-linux-arm"}
	case "linux/386":
		return []string{"cloudflared-linux-386"}
	case "windows/amd64":
		return []string{"cloudflared-windows-amd64.exe"}
	case "windows/386":
		return []string{"cloudflared-windows-386.exe"}
	case "windows/arm64":
		// 无原生 arm64 构建，amd64 版本在 Windows 上可兼容运行
		return []string{"cloudflared-windows-amd64.exe"}
	default:
		return nil
	}
}

// unsupportedPlatformError 在架构无官方构建时给出可操作的替代方案，
// 而不是一句"不支持的平台"让用户无从下手。
func unsupportedPlatformError() error {
	return fmt.Errorf(
		"当前架构没有 cloudflared 官方构建: %s/%s\n"+
			"  Cloudflare 仅提供 Linux 的 amd64 / arm64 / arm(armhf) / 386，不支持 mips、mipsle、riscv64 等架构。\n"+
			"  可选方案:\n"+
			"    1) 改用中继模式（frp 覆盖全部上述架构）:\n"+
			"         cftunnelX relay init --server <服务器IP:端口> --token <token>\n"+
			"         cftunnelX relay add <名称> <本地端口> <远程端口>\n"+
			"         cftunnelX relay up\n"+
			"    2) 自行提供 cloudflared：放到 PATH 中、或放到依赖目录 %s、\n"+
			"       或用 CFTUNNEL_CLOUDFLARED_URL 指定下载地址",
		runtime.GOOS, runtime.GOARCH, config.BinDir())
}
