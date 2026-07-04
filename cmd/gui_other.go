//go:build !windows

package cmd

func detectWindowsGUI() bool {
	return false
}
