package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/teamworker/file-agent/internal/agent"
	"github.com/teamworker/file-agent/internal/terminal"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var appVersion = "0.8.3"

const NPMInstallGuide = "https://blog.csdn.net/weixin_41929531/article/details/158885541"

// App is the main application struct bound to the Wails frontend.
type App struct {
	ctx   context.Context
	agent *agent.Agent
	term  *terminal.Terminal
}

func NewApp() *App {
	return &App{
		agent: agent.NewAgent(),
		term:  terminal.NewTerminal(),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	terminal.EventEmitFunc = func(name string, data interface{}) {
		runtime.EventsEmit(ctx, name, data)
	}
}

// ── Connection ──────────────────────────────────────

func (a *App) Connect(code string, serverURL string, dir string) error {
	if code == "" {
		return fmt.Errorf("连接码不能为空")
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
		Title: "选择共享目录",
	})
	if err != nil {
		return ""
	}
	return dir
}

func (a *App) GetVersion() string {
	return appVersion
}

// ── Environment Detection ───────────────────────────

type EnvStatus struct {
	HasNPM bool   `json:"hasNpm"`
	NPMVer string `json:"npmVer"`
	HasPip bool   `json:"hasPip"`
	PipVer string `json:"pipVer"`
}

func (a *App) CheckEnvironment() EnvStatus {
	env := EnvStatus{}
	shell := userLoginShell()

	if out, err := exec.Command(shell, "-l", "-c", "which npm && npm --version").CombinedOutput(); err == nil {
		env.HasNPM = true
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 2 {
			env.NPMVer = strings.TrimSpace(lines[len(lines)-1])
		}
	}

	if out, err := exec.Command(shell, "-l", "-c", "which pip3 && pip3 --version").CombinedOutput(); err == nil {
		env.HasPip = true
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 2 {
			env.PipVer = strings.TrimSpace(lines[len(lines)-1])
		}
	} else if out, err := exec.Command(shell, "-l", "-c", "which pip && pip --version").CombinedOutput(); err == nil {
		env.HasPip = true
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 2 {
			env.PipVer = strings.TrimSpace(lines[len(lines)-1])
		}
	}

	return env
}

// ── Agent Management ────────────────────────────────

type ACPAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	InstallType string `json:"installType"`
	InstallCmd  string `json:"installCmd"`
	CheckCmd    string `json:"checkCmd"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version"`
	HelpURL     string `json:"helpUrl"`
	Selected    bool   `json:"selected"`
}

func (a *App) GetACPAgents() []ACPAgent {
	agents := []ACPAgent{
		{
			ID: "opencode", Name: "OpenCode", Icon: "🔧",
			InstallType: "npm",
			InstallCmd:  "npm install -g opencode-ai@latest",
			CheckCmd:    "opencode --version",
			HelpURL:     "https://opencode.ai",
		},
		{
			ID: "claude_code", Name: "Claude Code", Icon: "🤖",
			InstallType: "npm",
			InstallCmd:  "npm install -g @anthropic-ai/claude-code",
			CheckCmd:    "claude --version",
			HelpURL:     "https://docs.anthropic.com/en/docs/claude-code",
		},
		{
			ID: "codex", Name: "Codex CLI", Icon: "⚡",
			InstallType: "npm",
			InstallCmd:  "npm install -g @openai/codex",
			CheckCmd:    "codex --version",
			HelpURL:     "https://github.com/openai/codex",
		},
		{
			ID: "qwen_code", Name: "Qwen Code", Icon: "🧠",
			InstallType: "pip",
			InstallCmd:  "pip install qwen-code",
			CheckCmd:    "qwen --version",
			HelpURL:     "https://github.com/QwenLM/qwen-code",
		},
	}

	for i := range agents {
		cmd := exec.Command(userLoginShell(), "-l", "-c", agents[i].CheckCmd)
		output, err := cmd.CombinedOutput()
		if err == nil {
			agents[i].Installed = true
			agents[i].Version = strings.TrimSpace(string(output))
		}
	}
	return agents
}

func (a *App) GetNPMInstallGuide() string {
	return NPMInstallGuide
}

func (a *App) SetACPRunner(runner string) {
	a.agent.SetACPRunner(runner)
}

// ── Terminal ────────────────────────────────────────

func (a *App) TerminalStart() error {
	return a.term.Start(a.ctx)
}

func (a *App) TerminalWrite(data string) {
	a.term.Write(data)
}

func (a *App) TerminalResize(rows, cols uint16) {
	a.term.Resize(rows, cols)
}

func (a *App) TerminalClose() {
	a.term.Close()
}

func (a *App) TerminalRunCommand(cmd string) {
	if !a.term.IsRunning() {
		a.TerminalStart()
	}
	a.term.Write(cmd + "\n")
}

func (a *App) OpenURL(url string) {
	exec.Command("open", url).Start()
}

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
