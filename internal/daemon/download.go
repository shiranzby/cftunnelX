package daemon

import (
	"archive/tar"
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
	return filepath.Join(config.Dir(), "bin", name)
}

// EnsureCloudflared 确保 cloudflared 已安装，未安装则自动下载
func EnsureCloudflared() (string, error) {
	path := CloudflaredPath()
	if _, err := os.Stat(path); err == nil {
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
	// 尝试系统 PATH
	if p, err := exec.LookPath("cloudflared"); err == nil {
		return p, nil
	}
	return path, download(path)
}

func bundledCloudflaredPath() (string, bool) {
	return BundledCloudflaredPath()
}

// BundledCloudflaredPath returns a cloudflared.exe shipped beside cftunnel.
func BundledCloudflaredPath() (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, "bin", "cloudflared.exe"),
		filepath.Join(dir, "cloudflared.exe"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
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

func download(dest string) error {
	cloudflaredDownloadMu.Lock()
	defer cloudflaredDownloadMu.Unlock()
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	filename, err := downloadFilename()
	if err != nil {
		return err
	}
	const origin = "https://github.com/cloudflare/cloudflared/releases/latest/download/"
	fmt.Println("正在下载 cloudflared...")

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	client := &http.Client{Timeout: 120 * time.Second}
	var lastErr error
	for _, mirror := range mirrors {
		url := mirror + origin + filename
		src := "GitHub"
		if mirror != "" {
			src = strings.TrimRight(mirror, "/")
		}
		fmt.Printf("尝试下载: %s ...\n", src)

		resp, err := client.Get(url)
		if err != nil {
			fmt.Printf("  连接失败: %v\n", err)
			lastErr = err
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			fmt.Printf("  HTTP %d\n", resp.StatusCode)
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, src)
			continue
		}

		err = saveCloudflared(resp.Body, dest, filename)
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

func downloadFilename() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "cloudflared-darwin-arm64.tgz", nil
	case "darwin/amd64":
		return "cloudflared-darwin-amd64.tgz", nil
	case "linux/amd64":
		return "cloudflared-linux-amd64", nil
	case "linux/arm64":
		return "cloudflared-linux-arm64", nil
	case "windows/amd64":
		return "cloudflared-windows-amd64.exe", nil
	case "windows/arm64":
		return "cloudflared-windows-amd64.exe", nil
	default:
		return "", fmt.Errorf("不支持的平台: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}
