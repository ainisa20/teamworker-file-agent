//go:build windows

package agent

import (
	"os/exec"
	"syscall"
)

func setCmdAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
