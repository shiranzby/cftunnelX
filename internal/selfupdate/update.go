package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
)

const repo = "shiranzby/cftunnelX"

type release struct {
	TagName string `json:"tag_name"`
}

// LatestVersion 查询 GitHub 最新版本
func LatestVersion() (string, error) {
	resp, err := http.Get("https://api.github.com/repos/" + repo + "/releases/latest")
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

// Update 下载最新版本替换自身
func Update(version string) error {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/cftunnelX_%s_%s.%s",
		repo, version, runtime.GOOS, runtime.GOARCH, ext)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	var binData io.Reader
	if runtime.GOOS == "windows" {
		binData, err = extractZip(resp.Body)
	} else {
		binData, err = extractTarGz(resp.Body)
	}
	if err != nil {
		return err
	}

	// Windows 不允许覆盖运行中的 exe，先 rename 旧文件
	tmp := exe + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, binData); err != nil {
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

func extractTarGz(r io.Reader) (io.Reader, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("解压失败: %w", err)
	}
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("tar.gz 中未找到 cftunnel")
		}
		if err != nil {
			return nil, fmt.Errorf("解压失败: %w", err)
		}
		if hdr.Name == "cftunnelX" || hdr.Name == "cftunnel" {
			return tr, nil
		}
	}
}

func extractZip(r io.Reader) (io.Reader, error) {
	// zip 需要 ReaderAt，先写到临时文件
	tmp, err := os.CreateTemp("", "cftunnelX-update-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := io.Copy(tmp, r); err != nil {
		return nil, err
	}
	info, _ := tmp.Stat()
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
			return rc, nil
		}
	}
	return nil, fmt.Errorf("zip 中未找到 cftunnel.exe")
}
