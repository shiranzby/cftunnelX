package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"testing"
)

const wantPayload = "CFTUNNELX-BINARY-CONTENT"

// TestExtractTarGz 验证 tar.gz 解包能取到名为 cftunnelX 的条目。
func TestExtractTarGz(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte(wantPayload)
	// 先放一个无关条目，确认只挑目标文件
	if err := tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0644, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("read")); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "cftunnelX", Mode: 0755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractTarGz(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("extractTarGz 失败: %v", err)
	}
	if string(got) != wantPayload {
		t.Errorf("内容 = %q, 期望 %q", string(got), wantPayload)
	}
}

// TestExtractZip 验证 zip 解包。
//
// 这是 P1 缺陷的回归测试：旧实现把 zip 写入临时文件后立刻 defer 删除并关闭，
// 却把依赖该文件句柄的 reader 返回给调用方；在 Windows 上句柄已失效，
// 导致 `cftunnelX update` 必然失败。现在必须在临时文件有效期内读完内容。
func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("cftunnelX.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(wantPayload)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractZip(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("extractZip 失败: %v", err)
	}
	if string(got) != wantPayload {
		t.Errorf("内容 = %q, 期望 %q", string(got), wantPayload)
	}
}

// TestExtractMissing 缺少目标文件时返回错误而不是空内容。
func TestExtractMissing(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "other", Mode: 0644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := extractTarGz(bytes.NewReader(buf.Bytes())); err == nil {
		t.Error("tar.gz 不含 cftunnelX 时应返回错误")
	}
}
