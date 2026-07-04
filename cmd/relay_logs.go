package cmd

import (
	"bufio"
	"fmt"
	"os"
	"time"

	"github.com/shiranzby/cftunnelX/internal/relay"
	"github.com/spf13/cobra"
)

var relayLogsFollow bool

func init() {
	relayLogsCmd.Flags().BoolVarP(&relayLogsFollow, "follow", "f", false, "实时跟踪日志")
	relayCmd.AddCommand(relayLogsCmd)
}

var relayLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "查看中继客户端日志",
	RunE: func(cmd *cobra.Command, args []string) error {
		logFile := relay.LogFilePath()
		f, err := os.Open(logFile)
		if err != nil {
			return fmt.Errorf("日志文件不存在: %s", logFile)
		}
		defer f.Close()

		lines, err := tailLines(f, 100)
		if err != nil {
			return err
		}
		for _, line := range lines {
			fmt.Println(line)
		}

		if !relayLogsFollow {
			return nil
		}

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
