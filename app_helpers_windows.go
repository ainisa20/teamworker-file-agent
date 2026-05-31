//go:build windows

package main

import (
	"os/exec"
	"strings"
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
	return "where " + name
}

func runShellCommand(cmdStr string) ([]byte, error) {
	shell := userLoginShell()
	if strings.HasSuffix(strings.ToLower(shell), "powershell.exe") || strings.HasSuffix(strings.ToLower(shell), "pwsh.exe") {
		return exec.Command(shell, "-NoProfile", "-Command", cmdStr).CombinedOutput()
	}
	return exec.Command(shell, "/c", cmdStr).CombinedOutput()
}
