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
	return filepath.Join(config.Dir(), "bin", name)
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

func downloadFrp(dest, binary string) error {
	frpDownloadMu.Lock()
	defer frpDownloadMu.Unlock()
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	filename, err := frpFilename()
	if err != nil {
		return err
	}
	origin := fmt.Sprintf("https://github.com/fatedier/frp/releases/download/v%s/", frpVersion)
	fmt.Printf("正在下载 %s (v%s)...\n", binary, frpVersion)

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

		err = extractFrpBinary(resp.Body, dest, filename, binary)
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

func frpFilename() (string, error) {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	switch platform {
	case "darwin/arm64":
		return fmt.Sprintf("frp_%s_darwin_arm64.tar.gz", frpVersion), nil
	case "darwin/amd64":
		return fmt.Sprintf("frp_%s_darwin_amd64.tar.gz", frpVersion), nil
	case "linux/amd64":
		return fmt.Sprintf("frp_%s_linux_amd64.tar.gz", frpVersion), nil
	case "linux/arm64":
		return fmt.Sprintf("frp_%s_linux_arm64.tar.gz", frpVersion), nil
	case "windows/amd64":
		return fmt.Sprintf("frp_%s_windows_amd64.zip", frpVersion), nil
	case "windows/arm64":
		return fmt.Sprintf("frp_%s_windows_arm64.zip", frpVersion), nil
	default:
		return "", fmt.Errorf("不支持的平台: %s", platform)
	}
}
