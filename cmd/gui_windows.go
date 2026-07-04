package cmd

import "syscall"

func detectWindowsGUI() bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	hwnd, _, _ := getConsoleWindow.Call()
	return hwnd == 0
}
