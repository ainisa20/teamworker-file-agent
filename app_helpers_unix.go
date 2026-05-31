//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strings"
)

func userLoginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	if out, err := exec.Command("dscl", ".", "-read", os.Getenv("HOME"), "UserShell").CombinedOutput(); err == nil {
		parts := strings.Fields(string(out))
		if len(parts) >= 2 {
			return parts[len(parts)-1]
		}
	}
	return "/bin/zsh"
}

func openURL(url string) {
	exec.Command("open", url).Start()
}

func findCommand(name string) string {
	return "which " + name
}

func runShellCommand(cmdStr string) ([]byte, error) {
	shell := userLoginShell()
	return exec.Command(shell, "-l", "-c", cmdStr).CombinedOutput()
}
