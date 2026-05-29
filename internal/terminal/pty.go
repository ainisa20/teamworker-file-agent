package terminal

import (
	"context"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// Terminal manages a PTY subprocess for interactive shell access.
type Terminal struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	ptmx    *os.File
	cmd     *exec.Cmd
	running bool
}

// NewTerminal creates a new Terminal instance.
func NewTerminal() *Terminal {
	return &Terminal{}
}

// Start launches a PTY shell.
func (t *Terminal) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return nil
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}

	ctx, cancel := context.WithCancel(ctx)
	t.ctx = ctx
	t.cancel = cancel

	cmd := exec.CommandContext(ctx, shell, "-l")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return err
	}

	t.ptmx = ptmx
	t.cmd = cmd
	t.running = true

	go t.readOutput()
	go t.waitExit()

	return nil
}

// readOutput reads PTY output and pushes it to the frontend via EventEmitFunc.
func (t *Terminal) readOutput() {
	buf := make([]byte, 4096)
	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		n, err := t.ptmx.Read(buf)
		if err != nil {
			t.emit("terminal:exit", "")
			return
		}
		if n > 0 {
			t.emit("terminal:data", string(buf[:n]))
		}
	}
}

// waitExit waits for the subprocess to exit.
func (t *Terminal) waitExit() {
	err := t.cmd.Wait()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
	}

	t.mu.Lock()
	t.running = false
	t.mu.Unlock()

	t.emit("terminal:exit", map[string]interface{}{
		"code":  code,
		"error": "",
	})
}

// Write sends user input to the PTY.
func (t *Terminal) Write(data string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running || t.ptmx == nil {
		return nil
	}
	_, err := t.ptmx.Write([]byte(data))
	return err
}

// Resize adjusts the PTY dimensions.
func (t *Terminal) Resize(rows, cols uint16) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running || t.ptmx == nil {
		return nil
	}
	return pty.Setsize(t.ptmx, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

// Close shuts down the terminal.
func (t *Terminal) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	if t.ptmx != nil {
		t.ptmx.Close()
		t.ptmx = nil
	}
	t.running = false
}

// IsRunning returns whether the PTY subprocess is active.
func (t *Terminal) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// RunCommand writes a command string followed by newline to the PTY.
func (t *Terminal) RunCommand(ctx context.Context, cmdStr string) error {
	if !t.IsRunning() {
		if err := t.Start(ctx); err != nil {
			return err
		}
	}
	return t.Write(cmdStr + "\n")
}

// EventEmitFunc is set by app.go at startup to push events to the Wails frontend.
var EventEmitFunc func(name string, data interface{})

func (t *Terminal) emit(name string, data interface{}) {
	if EventEmitFunc != nil {
		EventEmitFunc(name, data)
	}
}
