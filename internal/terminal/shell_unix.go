//go:build !windows

package terminal

import "os"

func getDefaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/zsh"
}
