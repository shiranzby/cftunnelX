package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shiranzby/cftunnelX/internal/config"
	"github.com/spf13/cobra"
)

var follow bool

func init() {
	logsCmd.Flags().BoolVarP(&follow, "follow", "f", false, "实时跟踪日志")
	rootCmd.AddCommand(logsCmd)
}

// logFilePath 根据操作系统返回日志文件路径
func logFilePath() string {
	return filepath.Join(config.LogDir(), "cftunnelX.log")
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "查看隧道日志",
	RunE: func(cmd *cobra.Command, args []string) error {
		logFile := logFilePath()
		f, err := os.Open(logFile)
		if err != nil {
			return fmt.Errorf("日志文件不存在: %s", logFile)
		}
		defer f.Close()

		// 默认只输出最新 50 行，避免终端页首次进入时渲染过多日志卡顿。
		lines, err := tailLines(f, 50)
		if err != nil {
			return err
		}
		for _, line := range lines {
			fmt.Println(line)
		}

		if !follow {
			return nil
		}

		// 实时跟踪：轮询文件变化
		stat, _ := f.Stat()
		offset := stat.Size()
		for {
			f2, err := os.Open(logFile)
			if err != nil {
				continue
			}
			stat2, _ := f2.Stat()
			if stat2.Size() > offset {
				f2.Seek(offset, 0)
				scanner := bufio.NewScanner(f2)
				for scanner.Scan() {
					fmt.Println(scanner.Text())
				}
				offset = stat2.Size()
			}
			f2.Close()
			time.Sleep(500 * time.Millisecond)
		}
	},
}

// tailLines 读取文件最后 n 行
func tailLines(f *os.File, n int) ([]string, error) {
	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines, scanner.Err()
}
