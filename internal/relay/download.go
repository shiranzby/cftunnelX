package relay

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
)

const frpVersion = "0.66.0"

// GitHub 镜像源列表（与 daemon/download.go 保持一致）
var mirrors = []string{
	"https://ghfast.top/",
	"https://gh-proxy.com/",
	"https://ghproxy.cn/",
	"", // 原始 GitHub 地址
}

var frpDownloadMu sync.Mutex

// FrpcPath 返回 frpc 二进制路径
func FrpcPath() string {
	name := "frpc"
	if runtime.GOOS == "windows" {
		name = "frpc.exe"
	}
	return filepath.Join(config.BinDir(), name)
}

// FrpsPath 返回 frps 二进制路径
func FrpsPath() string {
	name := "frps"
	if runtime.GOOS == "windows" {
		name = "frps.exe"
	}
	return filepath.Join(config.Dir(), "bin", name)
}

// EnsureFrpc 确保 frpc 已安装，未安装则自动下载
func EnsureFrpc() (string, error) {
	path := FrpcPath()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return path, downloadFrp(path, "frpc")
}

// EnsureFrps 确保 frps 已安装，未安装则自动下载
func EnsureFrps() (string, error) {
	path := FrpsPath()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return path, downloadFrp(path, "frps")
}

// frpAssets 返回当前平台可用的 frp 发行资产名，按优先级排列。
//
// 资产名以 fatedier/frp 实际发布内容为准（v0.66.0 核对）。
// 覆盖 OpenWrt 常见架构：arm_cortex-a7(armhf)、mipsel_24kc、mips_24kc、
// aarch64_generic(arm64)、x86_64、riscv64，因此无 cloudflared 官方构建的
// 架构仍可通过中继模式使用本工具。
func frpAssets() []string {
	return frpAssetsFor(runtime.GOOS, runtime.GOARCH)
}

// frpAssetsFor 按 GOOS/GOARCH 返回资产名，便于对全部架构做单元测试。
func frpAssetsFor(goos, goarch string) []string {
	v := frpVersion
	switch goos + "/" + goarch {
	case "darwin/arm64":
		return []string{fmt.Sprintf("frp_%s_darwin_arm64.tar.gz", v)}
	case "darwin/amd64":
		return []string{fmt.Sprintf("frp_%s_darwin_amd64.tar.gz", v)}
	case "linux/amd64":
		return []string{fmt.Sprintf("frp_%s_linux_amd64.tar.gz", v)}
	case "linux/arm64":
		return []string{fmt.Sprintf("frp_%s_linux_arm64.tar.gz", v)}
	case "linux/arm":
		// 硬件浮点优先（OpenWrt arm_cortex-a7），否则回退软浮点
		return []string{
			fmt.Sprintf("frp_%s_linux_arm_hf.tar.gz", v),
			fmt.Sprintf("frp_%s_linux_arm.tar.gz", v),
		}
	case "linux/mips":
		return []string{fmt.Sprintf("frp_%s_linux_mips.tar.gz", v)}
	case "linux/mipsle":
		return []string{fmt.Sprintf("frp_%s_linux_mipsle.tar.gz", v)}
	case "linux/mips64":
		return []string{fmt.Sprintf("frp_%s_linux_mips64.tar.gz", v)}
	case "linux/mips64le":
		return []string{fmt.Sprintf("frp_%s_linux_mips64le.tar.gz", v)}
	case "linux/riscv64":
		return []string{fmt.Sprintf("frp_%s_linux_riscv64.tar.gz", v)}
	case "linux/loong64":
		return []string{fmt.Sprintf("frp_%s_linux_loong64.tar.gz", v)}
	case "windows/amd64":
		return []string{fmt.Sprintf("frp_%s_windows_amd64.zip", v)}
	case "windows/arm64":
		return []string{fmt.Sprintf("frp_%s_windows_arm64.zip", v)}
	default:
		return nil
	}
}

// frpDownloadURLs 返回待尝试的下载地址。
// 可用 CFTUNNEL_FRP_URL 指定自建镜像或离线包地址。
func frpDownloadURLs() ([]string, error) {
	if override := strings.TrimSpace(os.Getenv("CFTUNNEL_FRP_URL")); override != "" {
		fmt.Printf("使用 CFTUNNEL_FRP_URL 指定的下载地址\n")
		return []string{override}, nil
	}
	assets := frpAssets()
	if len(assets) == 0 {
		return nil, fmt.Errorf(
			"当前架构没有 frp 官方构建: %s/%s\n"+
				"  可设置 CFTUNNEL_FRP_URL 指向自建地址，或手工把 frpc/frps 放入 %s",
			runtime.GOOS, runtime.GOARCH, config.BinDir())
	}
	origin := fmt.Sprintf("https://github.com/fatedier/frp/releases/download/v%s/", frpVersion)
	urls := make([]string, 0, len(mirrors)*len(assets))
	for _, mirror := range mirrors {
		for _, asset := range assets {
			urls = append(urls, mirror+origin+asset)
		}
	}
	return urls, nil
}

func downloadFrp(dest, binary string) error {
	frpDownloadMu.Lock()
	defer frpDownloadMu.Unlock()
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := config.Ensure(); err != nil {
		return err
	}
	urls, err := frpDownloadURLs()
	if err != nil {
		return err
	}
	fmt.Printf("正在下载 %s (v%s)...\n", binary, frpVersion)

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

		nameHint := url
		if idx := strings.LastIndex(nameHint, "/"); idx >= 0 {
			nameHint = nameHint[idx+1:]
		}
		err = extractFrpBinary(resp.Body, dest, nameHint, binary)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		fmt.Printf("%s 已下载到 %s\n", binary, dest)
		return nil
	}
	return fmt.Errorf("所有下载源均失败，最后错误: %w", lastErr)
}

// extractFrpBinary 从压缩包中提取指定二进制文件
func extractFrpBinary(r io.Reader, dest, filename, binary string) error {
	tmp := dest + ".download"
	_ = os.Remove(tmp)
	defer os.Remove(tmp)
	var err error
	if strings.HasSuffix(filename, ".zip") {
		err = extractFrpZip(r, tmp, binary)
	} else {
		err = extractFrpTgz(r, tmp, binary)
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func extractFrpTgz(r io.Reader, dest, binary string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}
	defer gr.Close()

	target := binary
	if runtime.GOOS == "windows" {
		target = binary + ".exe"
	}

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("压缩包中未找到 %s", binary)
		}
		if err != nil {
			return fmt.Errorf("解压失败: %w", err)
		}
		if filepath.Base(hdr.Name) == target {
			f, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
			if runtime.GOOS != "windows" {
				os.Chmod(dest, 0755)
			}
			return nil
		}
	}
}

func extractFrpZip(r io.Reader, dest, binary string) error {
	// zip 需要先写入临时文件（zip.Reader 需要 ReaderAt）
	tmp, err := os.CreateTemp("", "frp-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := io.Copy(tmp, r); err != nil {
		return err
	}

	stat, _ := tmp.Stat()
	zr, err := zip.NewReader(tmp, stat.Size())
	if err != nil {
		return fmt.Errorf("解压 zip 失败: %w", err)
	}

	target := binary + ".exe"
	for _, f := range zr.File {
		if filepath.Base(f.Name) == target {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			defer out.Close()
			_, err = io.Copy(out, rc)
			return err
		}
	}
	return fmt.Errorf("zip 中未找到 %s", target)
}

// frpc / frps 的资产名映射见 frpAssets()。
