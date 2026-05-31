//go:build windows

package terminal

import "os/exec"

func getDefaultShell() string {
	if psh, err := exec.LookPath("powershell"); err == nil {
		return psh
	}
	if cmd, err := exec.LookPath("cmd"); err == nil {
		return cmd
	}
	return "cmd.exe"
}
