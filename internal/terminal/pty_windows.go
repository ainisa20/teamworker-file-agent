//go:build windows

package terminal

import (
	"context"
	"os"

	"github.com/UserExistsError/conpty"
)

type windowsProcess struct {
	cpty *conpty.ConPty
}

func (p *windowsProcess) read(buf []byte) (int, error) {
	return p.cpty.Read(buf)
}

func (p *windowsProcess) write(data []byte) (int, error) {
	return p.cpty.Write(data)
}

func (p *windowsProcess) resize(rows, cols uint16) error {
	return p.cpty.Resize(int(cols), int(rows))
}

func (p *windowsProcess) close() {
	p.cpty.Close()
}

func (p *windowsProcess) wait() (int, error) {
	exitCode, err := p.cpty.Wait(context.Background())
	return int(exitCode), err
}

func startProcess(ctx context.Context) (ptyProcess, error) {
	shell := getDefaultShell()

	cpty, err := conpty.Start(shell,
		conpty.ConPtyDimensions(80, 24),
		conpty.ConPtyEnv(append(os.Environ(), "TERM=xterm-256color")),
	)
	if err != nil {
		return nil, err
	}

	return &windowsProcess{cpty: cpty}, nil
}
