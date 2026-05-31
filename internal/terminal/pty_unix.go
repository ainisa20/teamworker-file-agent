//go:build !windows

package terminal

import (
	"context"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixProcess struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func (p *unixProcess) read(buf []byte) (int, error) {
	return p.ptmx.Read(buf)
}

func (p *unixProcess) write(data []byte) (int, error) {
	return p.ptmx.Write(data)
}

func (p *unixProcess) resize(rows, cols uint16) error {
	return pty.Setsize(p.ptmx, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

func (p *unixProcess) close() {
	p.ptmx.Close()
}

func (p *unixProcess) wait() (int, error) {
	err := p.cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), err
		}
		return 0, err
	}
	return 0, nil
}

func startProcess(ctx context.Context) (ptyProcess, error) {
	shell := getDefaultShell()

	cmd := exec.CommandContext(ctx, shell, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	return &unixProcess{
		ptmx: ptmx,
		cmd:  cmd,
	}, nil
}
