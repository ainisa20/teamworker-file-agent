//go:build !windows

package agent

import "os/exec"

func setCmdAttr(cmd *exec.Cmd) {}
