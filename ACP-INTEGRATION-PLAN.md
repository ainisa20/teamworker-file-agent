# file-agent ACP 智能体整合方案

> 将外部智能体（opencode / claude_code / codex / qwen_code）的远程调用能力整合进 file-agent，实现一条命令同时启动文件共享 + 代码执行双隧道。

## 目录

- [一、架构总览](#一架构总览)
- [二、用户界面设计](#二用户界面设计)
- [三、Go 后端改动](#三go-后端改动)
  - [3.1 新增 `internal/terminal/pty.go`](#31-新增-internalterminalptygo)
  - [3.2 改造 `app.go`](#32-改造-appgo)
  - [3.3 改造 `internal/agent/agent.go`](#33-改造-internalagentagentgo)
  - [3.4 改造 `cmd/file-agent/main.go`](#34-改造-cmdfile-agentmaingo)
  - [3.5 改造 `main.go`（Wails 窗口）](#35-改造-maingo-wails-窗口)
- [四、Vue 前端改动](#四vue-前端改动)
  - [4.1 `App.vue` 完整重写](#41-appvue-完整重写)
  - [4.2 `style.css` 新增样式](#42-stylecss-新增样式)
  - [4.3 `frontend/package.json` 新增 xterm.js](#43-frontendpackagejson-新增-xtermjs)
- [五、服务器端改动](#五服务器端改动)
  - [5.1 新增 `tcp_mux_server.py`](#51-新增-tcp_mux_serverpy)
  - [5.2 `stdio_bridge.py` 改造](#52-stdio_bridgepy-改造)
  - [5.3 `opencode_acp.sh` 改造](#53-opencode_acpsh-改造)
  - [5.4 `chisel-auth.json` 改造](#54-chisel-authjson-改造)
  - [5.5 QwenPaw ACP runner 配置](#55-qwenpaw-acp-runner-配置)
  - [5.6 TeamWorker API 扩展](#56-teamworker-api-扩展)
- [六、构建与分发](#六构建与分发)
- [七、端口分配规范](#七端口分配规范)
- [八、依赖清单](#八依赖清单)

---

## 一、架构总览

```
用户本地 Mac (file-agent 桌面应用):
┌─────────────────────────────────────────────────────┐
│  file-agent (Wails GUI)                              │
│                                                      │
│  ┌─ MCP server :18080 ─── 已有 ───────────────────┐ │
│  │  local_read_file / local_write_file / ...       │ │
│  └─────────────────────────────────────────────────┘ │
│                                                      │
│  ┌─ ACP bridge :4096 ─── 新增 ────────────────────┐ │
│  │  stdio_to_tcp.py (Python 子进程)                 │ │
│  │  → opencode acp / claude --acp / ...            │ │
│  │  (根据用户选择启动对应智能体)                      │ │
│  └─────────────────────────────────────────────────┘ │
│                                                      │
│  ┌─ Chisel client → server:7000 ─── 改造 ─────────┐ │
│  │  Remotes:                                        │ │
│  │    R:0.0.0.0:9101 → :18080  (MCP 文件共享)      │ │
│  │    R:0.0.0.0:9102 → :4096   (ACP 代码执行) ←新  │ │
│  └─────────────────────────────────────────────────┘ │
│                                                      │
│  ┌─ PTY Terminal ─── 新增 ─────────────────────────┐ │
│  │  xterm.js ↔ Wails Events ↔ Go pty               │ │
│  │  支持: npm install, 交互确认, 实时输出            │ │
│  └─────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────┘

服务器 Docker 网络:
┌─────────────────────────────────────────────────────┐
│  chisel-server :7000 (已有)                          │
│    :9101 → userA MCP   :9102 → userA ACP            │
│    :9103 → userB MCP   :9104 → userB ACP            │
│                                                      │
│  acp-mux :4099 (新增容器)                             │
│    RUNNER:userA → chisel-server:9102                 │
│    RUNNER:userB → chisel-server:9104                 │
│                                                      │
│  teamworker (172.19.0.10)                            │
│    → chisel-server:9101 → userA MCP (文件, 已有)     │
│                                                      │
│  qwenpaw (172.19.0.7)                               │
│    → teamworker → file-agent MCP (读文件)            │
│    → acp-mux:4099 → chisel:9102 → userA ACP (代码)  │
└─────────────────────────────────────────────────────┘
```

---

## 二、用户界面设计

### 断开状态（主界面）

```
┌──────────────────────────────────────────────────┐
│  📁 TeamWorker 文件共享 v0.8                      │
│                                                    │
│  ── 连接设置 ──────────────────────────────────   │
│  连接码: [ABCD-EFGH        ]                      │
│  共享目录: /Users/userA/project    [选择目录]     │
│                                                    │
│  ── 外部智能体 ────────────────────────────────   │
│                                                    │
│  ✅ npm 10.9.2 已就绪                              │
│                                                    │
│  ┌──────────┐ ┌──────────┐                        │
│  │ OpenCode │ │  Claude  │                        │
│  │ ✅v1.9.4 │ │  ⬜ 未装  │                        │
│  └──────────┘ └──────────┘                        │
│  ┌──────────┐ ┌──────────┐                        │
│  │  Codex   │ │   Qwen   │                        │
│  │  ⬜ 未装  │ │  ⬜ 未装  │                        │
│  └──────────┘ └──────────┘                        │
│                                                    │
│  [连接]                                            │
└──────────────────────────────────────────────────┘
```

### npm 未安装时

```
│  ── 外部智能体 ────────────────────────────────   │
│                                                    │
│  ⚠️ 未检测到 npm                                  │
│  [如何安装 Node.js/npm →]                          │
│                                                    │
│  ┌──────────┐ ┌──────────┐                        │
│  │ OpenCode │ │  Claude  │                        │
│  │  ⬜ 未装  │ │  ⬜ 未装  │                        │
│  └──────────┘ └──────────┘                        │
```

### 安装中的交互终端

```
│  ┌─ 终端 ──────────────────────────────────── ┐   │
│  │ $ npm install -g opencode-ai@latest         │   │
│  │                                              │   │
│  │ added 142 packages in 12s                   │   │
│  │                                              │   │
│  │ 63 packages are looking for funding          │   │
│  │   run `npm fund` for details                │   │
│  │                                              │   │
│  │ $ opencode --version                        │   │
│  │ opencode v1.9.4                             │   │
│  │ $ █                                         │   │
│  └──────────────────────────────────────────────┘   │
```

### 已连接状态

```
┌──────────────────────────────────────────────────┐
│  📁 TeamWorker 文件共享 v0.8                      │
│                                                    │
│  ✅ 已连接                                         │
│                                                    │
│  ┌────────────────────────────────────────────┐   │
│  │ 共享目录  /Users/userA/project             │   │
│  │ 连接码    ABCD-EFGH                        │   │
│  │ 智能体    OpenCode ✅                       │   │
│  │ 隧道      MCP :9101, ACP :9102             │   │
│  └────────────────────────────────────────────┘   │
│                                                    │
│  [断开连接]                                        │
└──────────────────────────────────────────────────┘
```

---

## 三、Go 后端改动

### 3.1 新增 `internal/terminal/pty.go`

可交互伪终端，通过 Wails Events 实现双向数据流。

```go
package terminal

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"github.com/creack/pty"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Terminal 管理一个 PTY 子进程
type Terminal struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	ptmx   *os.File
	cmd    *exec.Cmd
	running bool
}

// NewTerminal 创建终端实例
func NewTerminal() *Terminal {
	return &Terminal{}
}

// Start 启动一个 PTY shell
func (t *Terminal) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return nil
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	ctx, cancel := context.WithCancel(ctx)
	t.ctx = ctx
	t.cancel = cancel

	cmd := exec.CommandContext(ctx, shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return err
	}

	t.ptmx = ptmx
	t.cmd = cmd
	t.running = true

	// 读取 PTY 输出，通过 Wails Events 推送到前端
	go t.readOutput()

	// 等待进程退出
	go t.waitExit()

	return nil
}

// readOutput 循环读取 PTY 输出并推送到前端
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
			// PTY 关闭
			t.sendEvent("terminal:exit", "")
			return
		}
		if n > 0 {
			t.sendEvent("terminal:data", string(buf[:n]))
		}
	}
}

// waitExit 等待子进程退出
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

	t.sendEvent("terminal:exit", map[string]interface{}{
		"code":  code,
		"error": "",
	})
}

// Write 向 PTY 写入数据（用户输入）
func (t *Terminal) Write(data string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running || t.ptmx == nil {
		return nil
	}
	_, err := t.ptmx.Write([]byte(data))
	return err
}

// Resize 调整 PTY 大小
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

// Close 关闭终端
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

// IsRunning 返回终端是否在运行
func (t *Terminal) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// RunCommand 在 PTY 中执行单条命令
func (t *Terminal) RunCommand(ctx context.Context, cmdStr string) error {
	if !t.IsRunning() {
		if err := t.Start(ctx); err != nil {
			return err
		}
	}
	return t.Write(cmdStr + "\n")
}

// sendEvent 通过 Wails Events 发送事件
func (t *Terminal) sendEvent(name string, data interface{}) {
	// runtime.EventsEmit 需要 context，在 Start 时已保存
	// 此处用全局 app context，在 app.go 中设置
	if EventEmitFunc != nil {
		EventEmitFunc(name, data)
	}
}

// EventEmitFunc 由 app.go 在启动时设置
var EventEmitFunc func(name string, data interface{})
```

> **注意**：`EventEmitFunc` 是一个全局函数指针，由 `app.go` 在 `startup` 时设置为
> `runtime.EventsEmit(ctx, ...)` 的包装，避免 terminal 包直接依赖 Wails context。

### 3.2 改造 `app.go`

```go
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

var appVersion = "0.8.0"

const NPMInstallGuide = "https://blog.csdn.net/weixin_41929531/article/details/158885541"

// App 主应用结构体
type App struct {
	ctx      context.Context
	agent    *agent.Agent
	term     *terminal.Terminal
}

func NewApp() *App {
	return &App{
		agent: agent.NewAgent(),
		term:  terminal.NewTerminal(),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// 设置 terminal 的事件发送函数
	terminal.EventEmitFunc = func(name string, data interface{}) {
		runtime.EventsEmit(ctx, name, data)
	}
}

// ── 连接相关 ──────────────────────────────────────

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

// ── 环境检测 ──────────────────────────────────────

type EnvStatus struct {
	HasNPM  bool   `json:"hasNpm"`
	NPMVer  string `json:"npmVer"`
	HasPip  bool   `json:"hasPip"`
	PipVer  string `json:"pipVer"`
}

func (a *App) CheckEnvironment() EnvStatus {
	env := EnvStatus{}

	if path, err := exec.LookPath("npm"); err == nil {
		env.HasNPM = true
		_ = path
		out, _ := exec.Command("npm", "--version").CombinedOutput()
		env.NPMVer = strings.TrimSpace(string(out))
	}

	if _, err := exec.LookPath("pip"); err == nil {
		env.HasPip = true
		out, _ := exec.Command("pip", "--version").CombinedOutput()
		env.PipVer = strings.TrimSpace(string(out))
	}

	return env
}

// ── 智能体管理 ──────────────────────────────────────

type ACPAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	InstallType string `json:"installType"` // "npm" | "pip" | "brew"
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
		cmd := exec.Command("sh", "-c", agents[i].CheckCmd)
		output, err := cmd.CombinedOutput()
		if err == nil {
			agents[i].Installed = true
			agents[i].Version = strings.TrimSpace(string(output))
		}
	}
	return agents
}

// GetNPMInstallGuide 返回安装教程链接
func (a *App) GetNPMInstallGuide() string {
	return NPMInstallGuide
}

// ── 终端相关 ──────────────────────────────────────

// TerminalStart 启动 PTY 终端
func (a *App) TerminalStart() error {
	return a.term.Start(a.ctx)
}

// TerminalWrite 向终端写入数据
func (a *App) TerminalWrite(data string) {
	a.term.Write(data)
}

// TerminalResize 调整终端大小
func (a *App) TerminalResize(rows, cols uint16) {
	a.term.Resize(rows, cols)
}

// TerminalClose 关闭终端
func (a *App) TerminalClose() {
	a.term.Close()
}

// TerminalRunCommand 在终端中执行命令
func (a *App) TerminalRunCommand(cmd string) {
	if !a.term.IsRunning() {
		a.TerminalStart()
	}
	a.term.Write(cmd + "\n")
}

// OpenURL 在系统浏览器中打开链接
func (a *App) OpenURL(url string) {
	exec.Command("open", url).Start() // macOS
}
```

### 3.3 改造 `internal/agent/agent.go`

关键改动点：

**(a) ConnectConfig 新增 `ACPPort`**

```go
type ConnectConfig struct {
	Server   string `json:"server"`
	Port     int    `json:"port"`
	ACPPort  int    `json:"port_acp"`      // 新增: 0=不启用
	Auth     string `json:"auth"`
	User     string `json:"user"`
	MCPToken string `json:"mcp_token"`
	AgentID  string `json:"agent_id"`
	Code     string `json:"-"`
}
```

**(b) Agent 新增字段**

```go
type Agent struct {
	// ... 现有字段不变 ...

	// ACP bridge (新增)
	acpRunner    string        // 选中智能体: "opencode" / "claude_code" / ...
	acpLocalPort int           // 本地 bridge 端口, 默认 4096
	acpCmd       *exec.Cmd     // stdio_to_tcp.py 子进程
}

func (a *Agent) SetACPRunner(runner string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.acpRunner = runner
}
```

**(c) Start() 改造：chisel 双隧道**

```go
func (a *Agent) Start(dir string) error {
	a.mu.Lock()
	cfg := a.cfg
	serverURL := a.serverURL
	runner := a.acpRunner
	a.mu.Unlock()

	if cfg == nil {
		return fmt.Errorf("no connection config; call Connect first")
	}

	a.setState("connecting", "Starting MCP server and tunnel...")

	// --- MCP server (不变) ---
	localAddr := fmt.Sprintf("127.0.0.1:%d", a.localPort)
	tunnelURL := fmt.Sprintf("http://chisel-server:%d", cfg.Port)
	mcpHandler := mcp.NewMCPHandler(dir, cfg.MCPToken, tunnelURL, serverURL, cfg.Code)
	a.mcpServer = &http.Server{
		Addr:    localAddr,
		Handler: mcpHandler,
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	a.cancel = cancel

	go func() {
		if err := a.mcpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("MCP server error: %v", err)
		}
	}()

	// --- Chisel Remotes ---
	remotes := []string{
		fmt.Sprintf("R:0.0.0.0:%d:127.0.0.1:%d", cfg.Port, a.localPort),
	}
	tunnelInfo := fmt.Sprintf("MCP: :%d → :%d", cfg.Port, a.localPort)

	// --- ACP bridge (新增) ---
	if cfg.ACPPort > 0 && runner != "" {
		if err := a.startACPBridge(runner); err != nil {
			log.Printf("⚠ ACP bridge 启动失败 (文件共享仍可用): %v", err)
		} else {
			remotes = append(remotes,
				fmt.Sprintf("R:0.0.0.0:%d:127.0.0.1:%d", cfg.ACPPort, a.acpLocalPort))
			tunnelInfo += fmt.Sprintf(", ACP: :%d → :%d (%s)", cfg.ACPPort, a.acpLocalPort, runner)
		}
	}

	chiselConfig := &chclient.Config{
		Server:    cfg.Server,
		Auth:      fmt.Sprintf("%s:%s", cfg.User, cfg.Auth),
		KeepAlive: a.keepAlive,
		Remotes:   remotes,
		Verbose:   a.verbose,
	}

	go a.reconnectLoop(ctx, chiselConfig, serverURL, cfg.Code)

	a.mu.Lock()
	a.state.Status = "connected"
	a.state.Message = "Tunnel established"
	a.state.TunnelInfo = tunnelInfo
	a.mu.Unlock()

	return nil
}
```

**(d) 新增 startACPBridge / stopACPBridge**

```go
func (a *Agent) startACPBridge(runner string) error {
	acpPort := 4096
	a.acpLocalPort = acpPort

	// 查找 stdio_to_tcp.py
	script := findScript("stdio_to_tcp.py")
	if script == "" {
		return fmt.Errorf("stdio_to_tcp.py not found (需与 file-agent 同目录)")
	}

	ctx := a.ctx
	cmd := exec.CommandContext(ctx, "python3", script,
		"--port", fmt.Sprintf("%d", acpPort),
		"--hostname", "127.0.0.1",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start stdio_to_tcp.py: %w", err)
	}

	a.acpCmd = cmd
	log.Printf("✓ ACP bridge started on :%d for %s (PID: %d)", acpPort, runner, cmd.Process.Pid)
	return nil
}

func (a *Agent) stopACPBridge() {
	if a.acpCmd != nil && a.acpCmd.Process != nil {
		a.acpCmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- a.acpCmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			a.acpCmd.Process.Kill()
		}
		a.acpCmd = nil
	}
}

func findScript(name string) string {
	// 1. 二进制同目录
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// 2. 当前工作目录
	if _, err := os.Stat(name); err == nil {
		return name
	}
	// 3. acp-bridge/ 子目录
	if _, err := os.Stat("acp-bridge/" + name); err == nil {
		return "acp-bridge/" + name
	}
	return ""
}
```

**(e) Stop() 增加 ACP 清理**

```go
func (a *Agent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.mcpServer != nil {
		a.mcpServer.Close()
		a.mcpServer = nil
	}
	a.stopACPBridge() // 新增

	a.state = State{Status: "disconnected"}
}
```

### 3.4 改造 `cmd/file-agent/main.go`

CLI 版本同步改造。关键改动点：

**(a) ConnectConfig 同步新增 `ACPPort`**

```go
type ConnectConfig struct {
	Server   string `json:"server"`
	Port     int    `json:"port"`
	ACPPort  int    `json:"port_acp"`      // 新增
	Auth     string `json:"auth"`
	User     string `json:"user"`
	MCPToken string `json:"mcp_token"`
	AgentID  string `json:"agent_id"`
	Code     string `json:"-"`
}
```

**(b) startAgent() 新增双隧道**

```go
func startAgent(server, authToken, user string, tPort, localPort int,
	mToken, dir string, keepAlive time.Duration, verbose bool,
	serverURL, code string, cfg *ConnectConfig) {

	// ... 目录验证不变 ...

	// MCP server (不变)
	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	tunnelURL := fmt.Sprintf("http://chisel-server:%d", tPort)
	mcpHandler := mcp.NewMCPHandler(absDir, mToken, tunnelURL, serverURL, code)
	mcpServer := &http.Server{Addr: localAddr, Handler: mcpHandler}

	go func() {
		if err := mcpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("✗ MCP 服务器错误: %v\n", err)
			os.Exit(1)
		}
	}()

	// 构建 Remotes
	remotes := []string{
		fmt.Sprintf("R:0.0.0.0:%d:127.0.0.1:%d", tPort, localPort),
	}
	fmt.Println("✓ 隧道已建立！")
	fmt.Printf("  MCP: R:127.0.0.1:%d → :%d (文件共享)\n", tPort, localPort)

	// ACP bridge (新增)
	if cfg != nil && cfg.ACPPort > 0 {
		scriptPath := findScriptCLI("stdio_to_tcp.py")
		if scriptPath != "" {
			acpLocalPort := 4096
			ctx := context.Background()
			acpCmd := exec.CommandContext(ctx, "python3", scriptPath,
				"--port", fmt.Sprintf("%d", acpLocalPort),
				"--hostname", "127.0.0.1",
			)
			acpCmd.Stdout = os.Stdout
			acpCmd.Stderr = os.Stderr
			if err := acpCmd.Start(); err == nil {
				remotes = append(remotes,
					fmt.Sprintf("R:0.0.0.0:%d:127.0.0.1:%d", cfg.ACPPort, acpLocalPort))
				fmt.Printf("  ACP: R:127.0.0.1:%d → :%d (代码执行)\n", cfg.ACPPort, acpLocalPort)
			} else {
				fmt.Printf("⚠ ACP bridge 启动失败: %v\n", err)
			}
		}
	}

	fmt.Println("  按 Ctrl+C 断开连接")

	chiselConfig := &chclient.Config{
		Server:    server,
		Auth:      fmt.Sprintf("%s:%s", user, authToken),
		KeepAlive: keepAlive,
		Remotes:   remotes,
		Verbose:   verbose,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconnectLoop(ctx, chiselConfig, serverURL, code)
	<-sigCh

	fmt.Println("\n正在断开连接...")
	cancel()
	mcpServer.Close()
}
```

### 3.5 改造 `main.go`（Wails 窗口）

```go
func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "TeamWorker 文件共享",
		Width:  520,                     // 改大: 480 → 520
		Height: 820,                     // 改大: 640 → 820
		// Width:  1024,                  // 调试时可开大
		// Height: 900,
		Frameless: false,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 245, G: 245, B: 245, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
```

---

## 四、Vue 前端改动

### 4.1 `App.vue` 完整重写

```vue
<script setup lang="ts">
import { ref, reactive, onMounted, onUnmounted, computed, nextTick, watch } from 'vue'
import { Terminal } from 'xterm'
import { FitAddon } from 'xterm-addon-fit'
import 'xterm/css/xterm.css'

// ── 类型 ──────────────────────────────────────────

interface AgentState {
  status: string
  message: string
  code: string
  sharedDir: string
  serverUrl: string
  tunnelInfo: string
  acpEnabled: boolean
}

interface EnvStatus {
  hasNpm: boolean
  npmVer: string
  hasPip: boolean
  pipVer: string
}

interface ACPAgent {
  id: string
  name: string
  icon: string
  installType: string
  installCmd: string
  installed: boolean
  version: string
  helpUrl: string
}

type AppState = 'disconnected' | 'connecting' | 'connected'

const NPM_GUIDE = 'https://blog.csdn.net/weixin_41929531/article/details/158885541'

// ── Wails 绑定 ──────────────────────────────────────

const wails = (window as any)['go']['main']['App']

// ── 状态 ──────────────────────────────────────────

const appState = ref<AppState>('disconnected')
const codeInput = ref('')
const selectedDir = ref('.')
const errorMessage = ref('')
const version = ref('v0.8.0')
const envStatus = ref<EnvStatus>({ hasNpm: false, npmVer: '', hasPip: false, pipVer: '' })
const agents = ref<ACPAgent[]>([])
const selectedAgent = ref('')
const showTerminal = ref(false)
const terminalReady = ref(false)

let pollTimer: ReturnType<typeof setInterval> | null = null
let xterm: Terminal | null = null
let fitAddon: FitAddon | null = null

// ── 计算属性 ──────────────────────────────────────

const statusLabel = computed(() => {
  switch (appState.value) {
    case 'connecting': return '正在连接...'
    case 'connected': return '已连接'
    default: return '未连接'
  }
})

const hasAnyInstalled = computed(() => agents.value.some(a => a.installed))

const installBlocked = computed(() => {
  if (!agents.value) return false
  // npm 类智能体未装且 npm 不可用
  return !envStatus.value.hasNpm
})

// ── 初始化 ──────────────────────────────────────

async function loadEnv() {
  try {
    envStatus.value = await wails.CheckEnvironment()
  } catch (e) { console.error(e) }
}

async function loadAgents() {
  try {
    agents.value = await wails.GetACPAgents()
    const installed = agents.value.find((a: ACPAgent) => a.installed)
    if (installed) selectedAgent.value = installed.id
  } catch (e) { console.error(e) }
}

async function fetchVersion() {
  try {
    const v = await wails.GetVersion()
    if (v) version.value = v
  } catch {}
}

// ── 终端 ──────────────────────────────────────

function initTerminal() {
  if (xterm) return

  const el = document.getElementById('terminal-container')
  if (!el) return

  xterm = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: '"SF Mono", Monaco, Menlo, Consolas, monospace',
    theme: {
      background: '#1e1e1e',
      foreground: '#d4d4d4',
      cursor: '#d4d4d4',
      selectionBackground: '#264f78',
    },
    rows: 8,
    cols: 60,
  })

  fitAddon = new FitAddon()
  xterm.loadAddon(fitAddon)
  xterm.open(el)
  fitAddon.fit()

  // 用户输入 → Go PTY
  xterm.onData((data: string) => {
    wails.TerminalWrite(data)
  })

  terminalReady.value = true
}

// 监听终端显示切换
watch(showTerminal, async (val) => {
  if (val) {
    await nextTick()
    initTerminal()
    if (!await wails.TerminalStart?.() !== undefined) {
      // TerminalStart may not exist yet, handle gracefully
    }
    setTimeout(() => fitAddon?.fit(), 100)
  }
})

// ── 智能体操作 ──────────────────────────────────────

async function installAgent(agent: ACPAgent) {
  if (agent.installType === 'npm' && !envStatus.value.hasNpm) {
    errorMessage.value = `安装 ${agent.name} 需要 npm。请先安装 Node.js 环境。`
    return
  }

  // 打开终端并执行安装命令
  showTerminal.value = true
  await nextTick()
  initTerminal()

  await wails.TerminalStart()
  xterm?.focus()
  wails.TerminalRunCommand(agent.installCmd)

  // 安装完成后刷新检测
  setTimeout(async () => {
    await loadAgents()
    await loadEnv()
  }, 3000)
}

function openGuide() {
  wails.OpenURL(NPM_GUIDE)
}

// ── 连接 ──────────────────────────────────────

function clearPolling() {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
}

function startPolling(ms: number) {
  clearPolling()
  pollTimer = setInterval(async () => {
    try {
      const state: AgentState = await wails.GetState()
      handleStateUpdate(state)
    } catch (e: any) { showError(e?.message || String(e)) }
  }, ms)
}

function handleStateUpdate(state: AgentState) {
  switch (state.status) {
    case 'connected':
      appState.value = 'connected'
      tunnelInfo.code = state.code
      tunnelInfo.sharedDir = state.sharedDir
      tunnelInfo.serverUrl = state.serverUrl
      tunnelInfo.acpEnabled = state.acpEnabled
      clearPolling()
      startPolling(1000)
      break
    case 'connecting':
      appState.value = 'connecting'
      break
    case 'disconnected':
      appState.value = 'disconnected'
      clearPolling()
      break
    case 'error':
      showError(state.message || '连接失败')
      appState.value = 'disconnected'
      clearPolling()
      break
  }
}

async function handleConnect() {
  if (!codeInput.value.trim()) { showError('请输入连接码'); return }
  clearError()

  appState.value = 'connecting'
  startPolling(500)

  try {
    const err = await wails.Connect(codeInput.value, '', selectedDir.value)
    if (err) {
      showError(typeof err === 'string' ? err : err?.message || '连接失败')
      appState.value = 'disconnected'
      clearPolling()
    }
  } catch (e: any) {
    showError(e?.message || String(e))
    appState.value = 'disconnected'
    clearPolling()
  }
}

async function handleDisconnect() {
  try { await wails.Disconnect() } catch {}
  appState.value = 'disconnected'
  tunnelInfo.code = ''
  tunnelInfo.sharedDir = ''
  tunnelInfo.serverUrl = ''
  clearPolling()
}

async function handleSelectDir() {
  try {
    const dir = await wails.SelectDirectory()
    if (dir) selectedDir.value = dir
  } catch {}
}

function showError(msg: string) { errorMessage.value = msg }
function clearError() { errorMessage.value = '' }
function handleRetry() { clearError(); appState.value = 'disconnected'; clearPolling() }

const tunnelInfo = reactive({
  code: '', sharedDir: '', serverUrl: '', acpEnabled: false,
})

// ── 生命周期 ──────────────────────────────────────

onMounted(() => {
  fetchVersion()
  loadEnv()
  loadAgents()

  // 监听 Go PTY 输出事件
  if ((window as any)['runtime']) {
    (window as any)['runtime'].EventsOn('terminal:data', (data: string) => {
      xterm?.write(data)
    })
    (window as any)['runtime'].EventsOn('terminal:exit', () => {
      xterm?.write('\r\n\x1b[33m[进程已退出]\x1b[0m\r\n')
    })
  }
})

onUnmounted(() => {
  clearPolling()
  wails.TerminalClose?.()
})
</script>

<template>
  <div class="app-container">
    <header class="header">
      <div class="header__logo">📁</div>
      <h1 class="header__title">TeamWorker 文件共享</h1>
      <div class="header__version">{{ version }}</div>
    </header>

    <Transition name="fade">
      <div v-if="errorMessage" class="error-banner">
        <span class="error-banner__icon">⚠️</span>
        <span class="error-banner__msg">{{ errorMessage }}</span>
        <button class="btn btn--sm btn--ghost" @click="handleRetry">重试</button>
      </div>
    </Transition>

    <!-- ═══ 未连接状态 ═══ -->
    <template v-if="appState === 'disconnected'">
      <div class="form-group">
        <label class="form-label">连接码</label>
        <input v-model="codeInput" class="input"
          placeholder="ABCD-EFGH 或 ABCD-EFGH@http://server:8082"
          @keyup.enter="handleConnect" />
      </div>

      <div class="form-group">
        <label class="form-label">共享目录</label>
        <div class="dir-selector">
          <span class="dir-selector__path" :title="selectedDir">{{ selectedDir }}</span>
          <button class="dir-selector__btn" @click="handleSelectDir">选择目录</button>
        </div>
      </div>

      <div class="divider" />

      <!-- 智能体区域 -->
      <div class="form-group">
        <label class="form-label">外部智能体</label>

        <div v-if="envStatus.hasNpm" class="env-ok">
          ✅ npm {{ envStatus.npmVer }}
        </div>
        <div v-else class="env-warning">
          <span>⚠️ 未检测到 npm</span>
          <a class="env-warning__link" href="#" @click.prevent="openGuide">
            如何安装 Node.js/npm →
          </a>
        </div>

        <div class="agent-grid">
          <div v-for="agent in agents" :key="agent.id"
            class="agent-card"
            :class="{
              'agent-card--selected': selectedAgent === agent.id,
              'agent-card--installed': agent.installed,
              'agent-card--not-installed': !agent.installed,
            }"
            @click="agent.installed && (selectedAgent = agent.id)"
          >
            <div class="agent-card__icon">{{ agent.icon }}</div>
            <div class="agent-card__name">{{ agent.name }}</div>
            <div class="agent-card__status">
              <span v-if="agent.installed" class="agent-installed">
                ✅ {{ agent.version?.split('\n')[0]?.substring(0, 12) }}
              </span>
              <span v-else class="agent-not-installed">未安装</span>
            </div>
            <button v-if="!agent.installed"
              class="btn btn--sm btn--primary agent-card__install"
              :disabled="agent.installType === 'npm' && !envStatus.hasNpm"
              @click.stop="installAgent(agent)"
            >
              {{ agent.installType === 'npm' && !envStatus.hasNpm ? '需 npm' : '安装' }}
            </button>
          </div>
        </div>

        <!-- npm 缺失提示 -->
        <Transition name="fade">
          <div v-if="installBlocked && !envStatus.hasNpm" class="npm-help-banner">
            <div class="npm-help-banner__icon">💡</div>
            <div class="npm-help-banner__content">
              <p>安装智能体需要 <strong>npm</strong>（Node.js 包管理器）。</p>
              <p>请先安装 Node.js，然后重启本应用。</p>
              <a class="npm-help-banner__link" href="#" @click.prevent="openGuide">
                📖 Node.js / npm 安装教程 →
              </a>
            </div>
          </div>
        </Transition>
      </div>

      <!-- 终端 -->
      <div class="form-group">
        <div class="terminal-header" @click="showTerminal = !showTerminal">
          <span class="form-label" style="cursor:pointer">
            {{ showTerminal ? '▼ 终端' : '▶ 终端' }}
          </span>
        </div>
        <Transition name="fade">
          <div v-if="showTerminal" class="terminal-wrapper">
            <div id="terminal-container" class="terminal-xterm" />
          </div>
        </Transition>
      </div>

      <div class="mt-auto">
        <button class="btn btn--primary btn--block"
          :disabled="!codeInput.trim() || (!selectedAgent && agents.some(a => a.installed))"
          @click="handleConnect"
        >
          连接
        </button>
      </div>
    </template>

    <!-- ═══ 连接中 ═══ -->
    <template v-if="appState === 'connecting'">
      <div class="spinner-area">
        <div class="spinner" />
        <span class="spinner-area__text">正在连接...</span>
      </div>
      <div class="mt-auto">
        <button class="btn btn--ghost btn--block" @click="handleRetry">取消</button>
      </div>
    </template>

    <!-- ═══ 已连接 ═══ -->
    <template v-if="appState === 'connected'">
      <div class="status-badge status-badge--connected">
        <span class="status-badge__dot" />
        <span>已连接</span>
      </div>

      <div class="info-card">
        <div class="info-card__row">
          <span class="info-card__label">共享目录</span>
          <span class="info-card__value" :title="tunnelInfo.sharedDir">
            {{ tunnelInfo.sharedDir }}
          </span>
        </div>
        <div class="info-card__row">
          <span class="info-card__label">连接码</span>
          <span class="info-card__value">{{ tunnelInfo.code }}</span>
        </div>
        <div class="info-card__row">
          <span class="info-card__label">服务器</span>
          <span class="info-card__value">{{ tunnelInfo.serverUrl }}</span>
        </div>
        <div v-if="selectedAgent" class="info-card__row">
          <span class="info-card__label">智能体</span>
          <span class="info-card__value">{{ selectedAgent }}</span>
        </div>
        <div class="info-card__row">
          <span class="info-card__label">隧道</span>
          <span class="info-card__value info-card__value--small">
            {{ tunnelInfo.acpEnabled ? 'MCP + ACP' : 'MCP only' }}
          </span>
        </div>
      </div>

      <div class="mt-auto">
        <button class="btn btn--danger-outline btn--block" @click="handleDisconnect">
          断开连接
        </button>
      </div>
    </template>
  </div>
</template>
```

### 4.2 `style.css` 新增样式

在现有 `style.css` 末尾追加：

```css
/* ===== Agent Grid ===== */
.agent-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-sm);
}

.agent-card {
  position: relative;
  padding: var(--space-sm) var(--space-md);
  border: 2px solid var(--color-border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--transition-fast);
  text-align: center;
  user-select: none;
}

.agent-card--installed {
  border-color: #b7eb8f;
}

.agent-card--installed.agent-card--selected {
  background: #f6ffed;
  border-color: var(--color-success);
  box-shadow: 0 0 0 2px rgba(82, 196, 26, 0.2);
}

.agent-card--not-installed {
  opacity: 0.75;
  cursor: default;
}

.agent-card--not-installed:hover {
  opacity: 0.9;
}

.agent-card__icon {
  font-size: 18px;
  margin-bottom: 2px;
}

.agent-card__name {
  font-weight: 600;
  font-size: 12px;
}

.agent-card__status {
  font-size: 10px;
  color: var(--color-text-secondary);
  margin-top: 2px;
  min-height: 16px;
}

.agent-installed {
  color: var(--color-success);
}

.agent-not-installed {
  color: var(--color-text-placeholder);
}

.agent-card__install {
  margin-top: 4px;
  font-size: 11px !important;
  padding: 3px 10px !important;
}

/* ===== Environment Status ===== */
.env-ok {
  font-size: 12px;
  color: var(--color-success);
  margin-bottom: var(--space-xs);
}

.env-warning {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  background: #fff7e6;
  border: 1px solid #ffe58f;
  border-radius: var(--radius-md);
  font-size: 13px;
  color: #ad6800;
  margin-bottom: var(--space-sm);
}

.env-warning__link {
  color: var(--color-primary);
  text-decoration: none;
  font-size: 12px;
  white-space: nowrap;
}

.env-warning__link:hover {
  text-decoration: underline;
}

/* ===== npm Help Banner ===== */
.npm-help-banner {
  display: flex;
  gap: var(--space-md);
  padding: var(--space-md);
  background: #e6f7ff;
  border: 1px solid #91d5ff;
  border-radius: var(--radius-lg);
  font-size: 13px;
  line-height: 1.6;
  margin-top: var(--space-sm);
}

.npm-help-banner__icon {
  font-size: 20px;
  flex-shrink: 0;
}

.npm-help-banner__content p {
  margin: 0 0 var(--space-xs) 0;
}

.npm-help-banner__link {
  display: inline-block;
  margin-top: var(--space-xs);
  padding: 6px 14px;
  background: var(--color-primary);
  color: #fff;
  border-radius: var(--radius-sm);
  text-decoration: none;
  font-size: 13px;
  font-weight: 500;
}

.npm-help-banner__link:hover {
  opacity: 0.9;
}

/* ===== Terminal ===== */
.terminal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  cursor: pointer;
  user-select: none;
  padding: var(--space-xs) 0;
}

.terminal-wrapper {
  border-radius: var(--radius-md);
  overflow: hidden;
  margin-top: var(--space-xs);
}

.terminal-xterm {
  background: #1e1e1e;
  padding: 4px;
  min-height: 160px;
}

.terminal-xterm .xterm {
  padding: 4px;
}

/* ===== Divider ===== */
.divider {
  height: 1px;
  background: var(--color-divider);
  margin: var(--space-sm) 0;
}

/* ===== Small Value ===== */
.info-card__value--small {
  font-size: 11px;
}
```

### 4.3 `frontend/package.json` 新增 xterm.js

```json
{
  "dependencies": {
    "vue": "^3.2.37",
    "xterm": "^5.3.0",
    "xterm-addon-fit": "^0.8.0"
  }
}
```

安装：
```bash
cd frontend && npm install xterm xterm-addon-fit
```

---

## 五、服务器端改动

### 5.1 新增 `tcp_mux_server.py`

部署位置：`/opt/acp-mux/tcp_mux_server.py`

```python
#!/usr/bin/env python3
"""TCP multiplexer for multi-user ACP routing.

Listens on port 4099 (single port).
Routes connections based on RUNNER:xxx header to per-user chisel tunnels.
Also provides HTTP management API on the same port.
"""
import socket
import select
import json
import os
import sys
import threading

ROUTES_FILE = os.environ.get("ROUTES_FILE", "/opt/acp-mux/routes.json")
MUX_PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 4099


def load_routes():
    try:
        with open(ROUTES_FILE) as f:
            return json.load(f)
    except Exception:
        return {}


def save_routes(routes):
    with open(ROUTES_FILE, "w") as f:
        json.dump(routes, f, indent=2, ensure_ascii=False)


def handle_acp(conn, runner_id, leftover):
    """ACP proxy: route to backend chisel tunnel."""
    routes = load_routes()
    route = routes.get(runner_id)
    if not route:
        try:
            conn.sendall(f"ERROR: unknown runner '{runner_id}'\n".encode())
        except OSError:
            pass
        conn.close()
        return

    host = route.get("backend_host", "127.0.0.1")
    port = route.get("backend_port", 0)
    if not port:
        try:
            conn.sendall(b"ERROR: runner has no backend port\n")
        except OSError:
            pass
        conn.close()
        return

    backend = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    backend.settimeout(10)
    try:
        backend.connect((host, port))
    except Exception as e:
        try:
            conn.sendall(f"ERROR: backend connect failed: {e}\n".encode())
        except OSError:
            pass
        conn.close()
        return
    backend.settimeout(None)

    # Forward leftover data (CWD line + initial ACP data)
    if leftover:
        try:
            backend.sendall(leftover)
        except OSError:
            conn.close()
            backend.close()
            return

    # Bidirectional proxy using select()
    try:
        client_fd = conn.fileno()
        backend_fd = backend.fileno()
        while True:
            r, _, _ = select.select([client_fd, backend_fd], [], [], 120.0)
            if not r:
                continue
            if client_fd in r:
                data = conn.recv(65536)
                if not data:
                    break
                backend.sendall(data)
            if backend_fd in r:
                data = backend.recv(65536)
                if not data:
                    break
                conn.sendall(data)
    except Exception:
        pass
    finally:
        try:
            backend.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        backend.close()
        conn.close()


def handle_http(conn, request_line, body_buf):
    """Simple HTTP management API."""
    parts = request_line.split(" ", 2)
    method = parts[0]
    path = parts[1] if len(parts) > 1 else "/"
    routes = load_routes()

    status = 200
    result = {}

    if path == "/mux/health":
        result = {"status": "ok", "runners": len(routes)}
    elif path == "/mux/routes" and method == "GET":
        result = routes
    elif path.startswith("/mux/routes/") and method == "DELETE":
        rid = path.split("/")[-1]
        if rid in routes:
            del routes[rid]
            save_routes(routes)
            result = {"deleted": rid}
        else:
            status = 404
            result = {"error": f"runner '{rid}' not found"}
    elif path == "/mux/routes" and method == "POST":
        try:
            new_route = json.loads(body_buf.decode("utf-8"))
            rid = new_route["runner_id"]
            routes[rid] = {
                "backend_host": new_route.get("backend_host", "chisel-server"),
                "backend_port": new_route["backend_port"],
                "description": new_route.get("description", ""),
            }
            save_routes(routes)
            result = {"registered": rid}
        except Exception as e:
            status = 400
            result = {"error": str(e)}
    else:
        status = 404
        result = {"error": "not found"}

    body = json.dumps(result, indent=2, ensure_ascii=False)
    resp = (
        f"HTTP/1.1 {status} OK\r\n"
        f"Content-Type: application/json\r\n"
        f"Content-Length: {len(body)}\r\n"
        f"Connection: close\r\n\r\n{body}"
    )
    try:
        conn.sendall(resp.encode())
    except OSError:
        pass
    conn.close()


def handle_client(conn, addr):
    """Classify and handle incoming connections."""
    conn.settimeout(10)
    try:
        first = conn.recv(65536)
    except socket.timeout:
        conn.close()
        return

    if not first:
        conn.close()
        return

    newline_pos = first.find(b"\n")
    if newline_pos == -1:
        first_line = first.decode("utf-8", errors="replace").strip()
        leftover = b""
    else:
        first_line = first[:newline_pos].decode("utf-8", errors="replace").strip()
        leftover = first[newline_pos + 1:]

    if first_line.startswith("RUNNER:"):
        runner_id = first_line[7:].strip()
        threading.Thread(
            target=handle_acp, args=(conn, runner_id, leftover), daemon=True
        ).start()
    elif first_line.startswith(("GET ", "POST ", "DELETE ", "PUT ")):
        handle_http(conn, first_line, leftover)
    else:
        try:
            conn.sendall(b"ERROR: invalid protocol\n")
        except OSError:
            pass
        conn.close()


def main():
    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind(("0.0.0.0", MUX_PORT))
    server.listen(20)
    print(f"[mux] Listening on 0.0.0.0:{MUX_PORT}", flush=True)
    print(f"[mux] Routes: {ROUTES_FILE}", flush=True)

    while True:
        conn, addr = server.accept()
        threading.Thread(target=handle_client, args=(conn, addr), daemon=True).start()


if __name__ == "__main__":
    main()
```

### 5.2 `stdio_bridge.py` 改造

改动点：新增第4个参数 `runner_id`，在 CWD 之前发送 `RUNNER:xxx\n`。

```diff
  def main():
      host = sys.argv[1] if len(sys.argv) > 1 else "127.0.0.1"
-     port = int(sys.argv[2]) if len(sys.argv) > 2 else 4096
+     port = int(sys.argv[2]) if len(sys.argv) > 2 else 4099
      local_cwd = sys.argv[3] if len(sys.argv) > 3 else ""
+     runner_id = sys.argv[4] if len(sys.argv) > 4 else ""
      if _is_container_path(local_cwd):
          local_cwd = ""

      sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
      sock.settimeout(10)
      try:
          sock.connect((host, port))
      except Exception as e:
          sys.stderr.write(f"connect failed: {e}\n")
          sys.exit(1)
      sock.settimeout(None)

+     # Send RUNNER (must be first line, before CWD)
+     if runner_id:
+         sock.sendall(f"RUNNER:{runner_id}\n".encode())
+         time.sleep(0.1)

      # Send CWD
      if local_cwd:
          sock.sendall(f"CWD:{local_cwd}\n".encode())
          time.sleep(0.3)
```

### 5.3 `opencode_acp.sh` 改造

```diff
  HOST="${1:-172.19.0.1}"
- PORT="${2:-4096}"
+ PORT="${2:-4099}"
+ RUNNER="${3:-${RUNNER_ID:-}}"
  CWD="${LOCAL_CWD:-/Users/vinson/Documents/www/team_demo}"
- exec python3 /opt/acp-bridge/stdio_bridge.py "$HOST" "$PORT" "$CWD"
+ exec python3 /opt/acp-bridge/stdio_bridge.py "$HOST" "$PORT" "$CWD" "$RUNNER"
```

### 5.4 `chisel-auth.json` 改造

每个用户从1条 ACL 变2条：

```json
{
  "userA:a9d78a2edd8c42f9b25e12cd": [
    "^R:(127\\.0\\.0\\.1|0\\.0\\.0\\.0):9101$",
    "^R:(127\\.0\\.0\\.1|0\\.0\\.0\\.0):9102$"
  ]
}
```

### 5.5 QwenPaw ACP runner 配置

```json
{
  "opencode_userA": {
    "enabled": true,
    "command": "bash",
    "args": ["/opt/acp-bridge/opencode_acp.sh", "acp-mux", "4099", "userA"],
    "env": {
      "LOCAL_CWD": "/Users/userA/project",
      "RUNNER_ID": "userA"
    },
    "trusted": true,
    "tool_parse_mode": "update_detail",
    "stdio_buffer_limit_bytes": 52428800
  }
}
```

### 5.6 TeamWorker API 扩展

`/api/tunnels/connect/:code` 返回新增 `port_acp`：

```json
{
  "server": "47.239.24.30:7000",
  "port": 9101,
  "port_acp": 9102,
  "auth": "a9d78a2edd8c42f9b25e12cd",
  "user": "userA",
  "mcp_token": "...",
  "agent_id": "..."
}
```

TeamWorker 内部逻辑：
1. 分配 MCP 端口（现有，如 9101）
2. `port_acp = port + 1`（如 9102）
3. chisel-auth.json 写入两条 ACL
4. routes.json 写入 runner → backend 映射

### 5.7 Docker Compose 新增 acp-mux 容器

在 `/data/site-copaw/docker-compose.yml` 新增：

```yaml
  acp-mux:
    image: python:3.11-alpine
    container_name: acp-mux
    command: python3 -u /opt/acp-mux/tcp_mux_server.py 4099
    environment:
      - ROUTES_FILE=/opt/acp-mux/routes.json
    volumes:
      - /opt/acp-mux:/opt/acp-mux
    networks:
      - likeadmin-net
    restart: unless-stopped
    deploy:
      resources:
        limits:
          cpus: '0.25'
          memory: 128M
```

---

## 六、构建与分发

### Go 依赖

```bash
cd /Users/vinson/Documents/www/teamworker-file-agent
go get github.com/creack/pty
```

### 前端依赖

```bash
cd frontend
npm install xterm xterm-addon-fit
```

### 打包

```bash
# 复制 stdio_to_tcp.py 到 dist
mkdir -p dist
cp /Users/vinson/Documents/www/team_demo/docs/acp-bridge/stdio_to_tcp.py dist/

# 编译 (嵌入 Wails GUI)
SERVER_URL="https://47.239.24.30:8082"

GOOS=darwin GOARCH=arm64 go build \
  -ldflags="-s -w -X main.defaultServerURL=${SERVER_URL}" \
  -o dist/file-agent-darwin-arm64

GOOS=darwin GOARCH=amd64 go build \
  -ldflags="-s -w -X main.defaultServerURL=${SERVER_URL}" \
  -o dist/file-agent-darwin-amd64

GOOS=linux GOARCH=amd64 go build \
  -ldflags="-s -w -X main.defaultServerURL=${SERVER_URL}" \
  -o dist/file-agent-linux-amd64

# CLI 版本 (无 GUI)
GOOS=darwin GOARCH=arm64 go build \
  -ldflags="-s -w -X main.defaultServerURL=${SERVER_URL}" \
  -o dist/file-agent-cli-darwin-arm64 ./cmd/file-agent
```

### 分发物

```
dist/
├── file-agent-darwin-arm64       # macOS GUI (M1/M2/M3)
├── file-agent-darwin-amd64       # macOS GUI (Intel)
├── file-agent-linux-amd64        # Linux GUI
├── file-agent-cli-darwin-arm64   # CLI 版本 (无 GUI)
└── stdio_to_tcp.py               # ACP bridge (需与二进制同目录)
```

---

## 七、端口分配规范

| 用户 | MCP 端口 | ACP 端口 | chisel ACL | mux 路由 |
|------|---------|---------|------------|---------|
| userA | 9101 | 9102 | `:9101`, `:9102` | `userA → :9102` |
| userB | 9103 | 9104 | `:9103`, `:9104` | `userB → :9104` |
| userC | 9105 | 9106 | `:9105`, `:9106` | `userC → :9106` |

规则：MCP 从 9101 起奇数递增，ACP = MCP + 1。

---

## 八、依赖清单

### Go 新增

| 包 | 用途 |
|----|------|
| `github.com/creack/pty` | Unix PTY 管理 |

### npm 新增 (前端)

| 包 | 用途 |
|----|------|
| `xterm` (v5.x) | 终端渲染 |
| `xterm-addon-fit` | 终端自适应大小 |

### Python (已有，不需新增)

| 脚本 | 用途 |
|------|------|
| `stdio_to_tcp.py` | opencode ACP stdio ↔ TCP 桥接 |

### 服务器新增

| 组件 | 用途 |
|------|------|
| `tcp_mux_server.py` | ACP 多路复用路由代理 |
| `routes.json` | 路由表配置 |
