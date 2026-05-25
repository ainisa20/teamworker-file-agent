package main

import (
	"context"
	"fmt"

	"github.com/teamworker/file-agent/internal/agent"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var appVersion = "0.7.0"

type App struct {
	ctx   context.Context
	agent *agent.Agent
}

func NewApp() *App {
	return &App{
		agent: agent.NewAgent(),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) Connect(code string, serverURL string, dir string) error {
	if code == "" {
		return fmt.Errorf("connection code cannot be empty")
	}

	parsedCode, parsedURL := agent.ParseConnectionString(code)
	if parsedURL != "" {
		serverURL = parsedURL
		code = parsedCode
	}

	if serverURL == "" {
		serverURL = defaultServerURL
	}

	if serverURL == "" {
		return fmt.Errorf("服务器地址不能为空，请在连接码后加上 @服务器地址")
	}

	return a.agent.Connect(code, serverURL, dir)
}

func (a *App) Disconnect() {
	a.agent.Stop()
}

func (a *App) GetState() agent.State {
	return a.agent.GetState()
}

func (a *App) SelectDirectory() string {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Shared Directory",
	})
	if err != nil {
		return ""
	}
	return dir
}

func (a *App) GetVersion() string {
	return appVersion
}
