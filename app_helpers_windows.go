//go:build windows

package main

import (
	"os/exec"
	"strings"
	"syscall"
)

func userLoginShell() string {
	if psh, err := exec.LookPath("powershell"); err == nil {
		return psh
	}
	return "cmd.exe"
}

func openURL(url string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func findCommand(name string) string {
	return "where.exe " + name
}

func runShellCommand(cmdStr string) ([]byte, error) {
	shell := userLoginShell()
	hideWindow := &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	if strings.HasSuffix(strings.ToLower(shell), "powershell.exe") || strings.HasSuffix(strings.ToLower(shell), "pwsh.exe") {
		cmd := exec.Command(shell, "-NoProfile", "-Command", cmdStr)
		cmd.SysProcAttr = hideWindow
		return cmd.CombinedOutput()
	}
	cmd := exec.Command(shell, "/c", cmdStr)
	cmd.SysProcAttr = hideWindow
	return cmd.CombinedOutput()
}
