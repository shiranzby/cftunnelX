package logutil

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	bracketRe = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]\s*([A-Z]+)?\s*(.*)$`)
	isoRe     = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})(?:\.\d+)?Z\s+([A-Z]+)\s*(.*)$`)
	writeMu   sync.Mutex
)

func Timestamp() string {
	return time.Now().Format("[2006-01-02 15:04:05]")
}

func Format(level, msg string) string {
	level = normalizeLevel(level, msg)
	return fmt.Sprintf("%s%s %s", Timestamp(), level, strings.TrimSpace(msg))
}

func Write(path, level, format string, args ...interface{}) {
	line := Format(level, fmt.Sprintf(format, args...))
	fmt.Println(line)
	Append(path, line)
}

func Append(path, line string) {
	if path == "" {
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(NormalizeLine(line) + "\n")
	_ = f.Close()
}

func NormalizeLine(line string) string {
	line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
	if line == "" {
		return ""
	}
	if m := bracketRe.FindStringSubmatch(line); m != nil {
		level := normalizeLevel(m[2], m[3])
		return fmt.Sprintf("[%s]%s %s", m[1], level, strings.TrimSpace(m[3]))
	}
	if m := isoRe.FindStringSubmatch(line); m != nil {
		level := normalizeLevel(m[3], m[4])
		return fmt.Sprintf("[%s %s]%s %s", m[1], m[2], level, strings.TrimSpace(m[4]))
	}
	return Format("", line)
}

func NormalizeLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if n := NormalizeLine(line); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func normalizeLevel(level, msg string) string {
	level = strings.ToUpper(strings.TrimSpace(level))
	switch level {
	case "INF":
		return "INFO"
	case "WRN", "WARNING":
		return "WARN"
	case "ERR":
		return "ERROR"
	case "INFO", "WARN", "ERROR", "DEBUG":
		return level
	}
	upper := strings.ToUpper(msg)
	switch {
	case strings.Contains(upper, "ERROR") || strings.Contains(upper, "FAILED") || strings.Contains(msg, "失败") || strings.Contains(msg, "错误"):
		return "ERROR"
	case strings.Contains(upper, "WARN") || strings.Contains(msg, "警告"):
		return "WARN"
	case strings.Contains(upper, "DEBUG"):
		return "DEBUG"
	default:
		return "INFO"
	}
}

type Writer struct {
	Path  string
	Level string
	mu    sync.Mutex
	buf   string
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf += string(p)
	for {
		i := strings.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(w.buf[:i], "\r")
		w.buf = w.buf[i+1:]
		if strings.TrimSpace(line) != "" {
			Append(w.Path, NormalizeLine(line))
		}
	}
	return len(p), nil
}
