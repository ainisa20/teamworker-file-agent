package terminal

import (
	"context"
	"sync"
)

type ptyProcess interface {
	read(buf []byte) (int, error)
	write(data []byte) (int, error)
	resize(rows, cols uint16) error
	close()
	wait() (exitCode int, err error)
}

type Terminal struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	proc    ptyProcess
	running bool
}

func NewTerminal() *Terminal {
	return &Terminal{}
}

func (t *Terminal) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	t.ctx = ctx
	t.cancel = cancel

	proc, err := startProcess(ctx)
	if err != nil {
		cancel()
		return err
	}

	t.proc = proc
	t.running = true

	go t.readOutput()
	go t.waitExit()

	return nil
}

func (t *Terminal) readOutput() {
	buf := make([]byte, 4096)
	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		n, err := t.proc.read(buf)
		if err != nil {
			t.emit("terminal:exit", "")
			return
		}
		if n > 0 {
			t.emit("terminal:data", string(buf[:n]))
		}
	}
}

func (t *Terminal) waitExit() {
	code, _ := t.proc.wait()

	t.mu.Lock()
	t.running = false
	t.mu.Unlock()

	t.emit("terminal:exit", map[string]interface{}{
		"code":  code,
		"error": "",
	})
}

func (t *Terminal) Write(data string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running || t.proc == nil {
		return nil
	}
	_, err := t.proc.write([]byte(data))
	return err
}

func (t *Terminal) Resize(rows, cols uint16) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running || t.proc == nil {
		return nil
	}
	return t.proc.resize(rows, cols)
}

func (t *Terminal) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	if t.proc != nil {
		t.proc.close()
		t.proc = nil
	}
	t.running = false
}

func (t *Terminal) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

func (t *Terminal) RunCommand(ctx context.Context, cmdStr string) error {
	if !t.IsRunning() {
		if err := t.Start(ctx); err != nil {
			return err
		}
	}
	return t.Write(cmdStr + "\n")
}

var EventEmitFunc func(name string, data interface{})

func (t *Terminal) emit(name string, data interface{}) {
	if EventEmitFunc != nil {
		EventEmitFunc(name, data)
	}
}
