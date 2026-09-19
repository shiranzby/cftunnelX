package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const repo = "shiranzby/cftunnelX"

type release struct {
	TagName string `json:"tag_name"`
}

// LatestVersion 查询 GitHub 最新版本
func LatestVersion() (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("查询失败: HTTP %d", resp.StatusCode)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	return r.TagName, nil
}

// mirrors 下载镜像（与 daemon / relay 保持一致），最后一项为 GitHub 原始地址。
var mirrors = []string{
	"https://ghfast.top/",
	"https://gh-proxy.com/",
	"https://ghproxy.cn/",
	"",
}

// downloadRelease 依次尝试镜像下载发行包，返回包体字节。
func downloadRelease(version string) ([]byte, error) {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	asset := fmt.Sprintf("cftunnelX_%s_%s.%s", runtime.GOOS, runtime.GOARCH, ext)
	origin := fmt.Sprintf("https://github.com/%s/releases/download/%s/", repo, version)

	client := &http.Client{Timeout: 300 * time.Second}
	var lastErr error
	for _, mirror := range mirrors {
		url := mirror + origin + asset
		src := "GitHub"
		if mirror != "" {
			src = strings.TrimRight(mirror, "/")
		}
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			fmt.Printf("  下载源 %s 失败: %v\n", src, err)
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, src)
			fmt.Printf("  下载源 %s 返回 HTTP %d\n", src, resp.StatusCode)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		fmt.Printf("  已从 %s 下载 %d 字节\n", src, len(body))
		return body, nil
	}
	return nil, fmt.Errorf("所有下载源均失败: %w", lastErr)
}

// Update 下载最新版本替换自身
func Update(version string) error {
	pkg, err := downloadRelease(version)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}

	var binData []byte
	if runtime.GOOS == "windows" {
		binData, err = extractZip(bytes.NewReader(pkg))
	} else {
		binData, err = extractTarGz(bytes.NewReader(pkg))
	}
	if err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// Windows 不允许覆盖运行中的 exe，先 rename 旧文件
	tmp := exe + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(binData); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	if runtime.GOOS != "windows" {
		os.Chmod(tmp, 0755)
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		os.Remove(old)
		os.Rename(exe, old)
	}
	return os.Rename(tmp, exe)
}

// extractTarGz 从 tar.gz 中取出可执行文件内容。
// 返回 []byte 而不是流式 reader，避免调用方在读取前底层流已被关闭。
func extractTarGz(r io.Reader) ([]byte, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("解压失败: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("tar.gz 中未找到 cftunnelX")
		}
		if err != nil {
			return nil, fmt.Errorf("解压失败: %w", err)
		}
		if hdr.Name == "cftunnelX" || hdr.Name == "cftunnel" {
			return io.ReadAll(tr)
		}
	}
}

// extractZip 从 zip 中取出可执行文件内容。
//
// 旧实现把 zip 写到临时文件后立刻 defer 删除并关闭，却把依赖该文件句柄的
// reader 返回给调用方；在 Windows 上句柄已失效，导致自更新必然失败。
// 现在在临时文件仍然有效时就把内容读入内存，从根本上消除该生命周期问题。
func extractZip(r io.Reader) ([]byte, error) {
	tmp, err := os.CreateTemp("", "cftunnelX-update-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := io.Copy(tmp, r); err != nil {
		return nil, err
	}
	info, err := tmp.Stat()
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(tmp, info.Size())
	if err != nil {
		return nil, fmt.Errorf("解压 zip 失败: %w", err)
	}
	for _, f := range zr.File {
		if f.Name == "cftunnelX.exe" || f.Name == "cftunnel.exe" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("zip 中未找到 cftunnelX.exe")
}
