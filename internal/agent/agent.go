package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	chclient "github.com/jpillora/chisel/client"
	"github.com/teamworker/file-agent/internal/mcp"
)

// ConnectConfig holds the tunnel configuration returned by the server.
type ConnectConfig struct {
	Server   string `json:"server"`
	Port     int    `json:"port"`
	ACPPort  int    `json:"port_acp"`
	Auth     string `json:"auth"`
	User     string `json:"user"`
	MCPToken string `json:"mcp_token"`
	AgentID  string `json:"agent_id"`
	Code     string `json:"-"`
}

// State represents the current agent state, exposed to the frontend via JSON.
type State struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	ServerURL  string `json:"serverUrl"`
	Code       string `json:"code"`
	SharedDir  string `json:"sharedDir"`
	TunnelInfo string `json:"tunnelInfo"`
	ACPEnabled bool   `json:"acpEnabled"`
}

// Agent holds all business logic for the file sharing agent.
type Agent struct {
	mu         sync.Mutex
	state      State
	cfg        *ConnectConfig
	serverURL  string

	localPort int
	keepAlive time.Duration
	verbose   bool

	mcpServer *http.Server
	ctx       context.Context
	cancel    context.CancelFunc

	acpRunner    string
	acpLocalPort int
	acpCmd       *exec.Cmd
	acpLogFile   *os.File
}

// NewAgent creates a new Agent instance.
func NewAgent() *Agent {
	// Redirect log output to file for GUI mode (no console)
	homeDir, _ := os.UserHomeDir()
	logPath := filepath.Join(homeDir, ".file-agent.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(f)
		log.Printf("[INIT] log file: %s", logPath)
	}

	return &Agent{
		state: State{
			Status: "disconnected",
		},
		localPort: 18080,
		keepAlive: 25 * time.Second,
	}
}

// SetLocalPort sets the local MCP server port.
func (a *Agent) SetLocalPort(port int) {
	a.localPort = port
}

// SetKeepAlive sets the keep-alive interval.
func (a *Agent) SetKeepAlive(d time.Duration) {
	a.keepAlive = d
}

// SetVerbose enables or disables verbose logging.
func (a *Agent) SetVerbose(v bool) {
	a.verbose = v
}

func (a *Agent) SetACPRunner(runner string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.acpRunner = runner
}

// ConnectByCode performs an HTTP GET to obtain tunnel configuration.
func ConnectByCode(serverURL, code string) (*ConnectConfig, error) {
	serverURL = strings.TrimRight(serverURL, "/")
	url := fmt.Sprintf("%s/api/tunnels/connect/%s", serverURL, code)

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: tr}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to server: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var cfg ConnectConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	log.Printf("[ACP-DEBUG] server config: server=%s, port=%d, port_acp=%d, user=%s, has_auth=%v, has_mcp_token=%v",
		cfg.Server, cfg.Port, cfg.ACPPort, cfg.User, cfg.Auth != "", cfg.MCPToken != "")

	if cfg.Server == "" || cfg.Port == 0 || cfg.Auth == "" || cfg.User == "" || cfg.MCPToken == "" {
		return nil, fmt.Errorf("server returned incomplete configuration")
	}

	return &cfg, nil
}

// ReportConnected notifies the server that the tunnel is connected.
func ReportConnected(serverURL, code string) error {
	url := fmt.Sprintf("%s/api/tunnels/connect/%s/connected", strings.TrimRight(serverURL, "/"), code)
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to report connected: %w", err)
	}
	resp.Body.Close()
	return nil
}

// ParseConnectionString parses "XXXX-XXXX@server" into code and serverURL.
func ParseConnectionString(input string) (code string, serverURL string) {
	parts := strings.SplitN(input, "@", 2)
	code = parts[0]
	if len(parts) == 2 && parts[1] != "" {
		serverURL = parts[1]
		if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
			serverURL = "https://" + serverURL
		}
	}
	return
}

// GetState returns the current agent state (safe for concurrent access).
func (a *Agent) GetState() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *Agent) setState(status, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Status = status
	a.state.Message = message
}

// Connect performs the full connection sequence: resolve code, configure, and start.
// Call Start() separately after this to begin the tunnel and MCP server.
func (a *Agent) Connect(code, serverURL, dir string) error {
	a.mu.Lock()
	a.state.Status = "connecting"
	a.state.Message = "Resolving connection..."
	a.state.Code = code
	a.state.ServerURL = serverURL
	a.mu.Unlock()

	cfg, err := ConnectByCode(serverURL, code)
	if err != nil {
		a.setState("error", fmt.Sprintf("Failed to get connection config: %v", err))
		return err
	}
	cfg.Code = code

	a.mu.Lock()
	a.cfg = cfg
	a.serverURL = serverURL
	a.mu.Unlock()

	// Validate directory
	absDir, err := filepath.Abs(dir)
	if err != nil {
		a.setState("error", fmt.Sprintf("Cannot resolve directory: %v", err))
		return err
	}

	info, err := os.Stat(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			a.setState("error", fmt.Sprintf("Directory does not exist: %s", absDir))
		} else {
			a.setState("error", fmt.Sprintf("Cannot access directory: %v", err))
		}
		return err
	}
	if !info.IsDir() {
		a.setState("error", fmt.Sprintf("Not a directory: %s", absDir))
		return fmt.Errorf("not a directory: %s", absDir)
	}

	a.mu.Lock()
	a.state.SharedDir = absDir
	a.mu.Unlock()

	// Start the agent
	return a.Start(absDir)
}

// Start validates the directory, creates MCPHandler, starts MCP server,
// and starts the chisel reconnect loop.
func (a *Agent) Start(dir string) error {
	a.mu.Lock()
	cfg := a.cfg
	serverURL := a.serverURL
	runner := a.acpRunner
	a.mu.Unlock()

	if cfg == nil {
		return fmt.Errorf("no connection config; call Connect first")
	}

	log.Printf("[ACP-DEBUG] cfg.ACPPort=%d, runner=%q", cfg.ACPPort, runner)

	if cfg.ACPPort > 0 && runner == "" {
		runner = "opencode"
		log.Printf("[ACP-DEBUG] no runner set, defaulting to %q", runner)
	}

	a.setState("connecting", "Starting MCP server and tunnel...")

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

	remotes := []string{
		fmt.Sprintf("R:0.0.0.0:%d:127.0.0.1:%d", cfg.Port, a.localPort),
	}
	tunnelInfo := fmt.Sprintf("MCP: :%d → :%d", cfg.Port, a.localPort)
	ACPEnabled := false

	log.Printf("[ACP-DEBUG] checking ACP: ACPPort=%d, runner=%q", cfg.ACPPort, runner)

	if cfg.ACPPort > 0 && runner != "" {
		log.Printf("[ACP-DEBUG] starting ACP bridge with runner=%q ...", runner)
		if err := a.startACPBridge(runner); err != nil {
			log.Printf("[ACP-DEBUG] ACP bridge failed: %v", err)
			tunnelInfo += fmt.Sprintf(" [ACP ERROR: %v]", err)
		} else {
			remotes = append(remotes,
				fmt.Sprintf("R:0.0.0.0:%d:127.0.0.1:%d", cfg.ACPPort, a.acpLocalPort))
			tunnelInfo += fmt.Sprintf(", ACP: :%d → :%d (%s)", cfg.ACPPort, a.acpLocalPort, runner)
			ACPEnabled = true
			log.Printf("[ACP-DEBUG] ACP enabled successfully")
		}
	} else {
		log.Printf("[ACP-DEBUG] ACP skipped: ACPPort=%d (need >0), runner=%q (need non-empty)", cfg.ACPPort, runner)
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
	a.state.ACPEnabled = ACPEnabled
	a.mu.Unlock()

	return nil
}

// Stop cancels the context and shuts down the MCP server.
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
	a.stopACPBridge()

	a.state = State{
		Status: "disconnected",
	}
}

func (a *Agent) startACPBridge(runner string) error {
	acpPort := 4096
	a.acpLocalPort = acpPort

	script, err := EnsureACPBridge()
	if err != nil {
		return fmt.Errorf("failed to extract ACP bridge script: %w", err)
	}

	homeDir, _ := os.UserHomeDir()
	logFile := filepath.Join(homeDir, ".file-agent-acp.log")
	logF, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logF = os.Stderr
	} else {
		log.Printf("ACP bridge log: %s", logFile)
	}
	fmt.Fprintf(logF, "\n=== %s ACP bridge starting ===\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(logF, "script=%s port=%d runner=%s\n", script, acpPort, runner)

	ctx := a.ctx

	pythonBin := "python3"
	if _, err := exec.LookPath("python3"); err != nil {
		if _, err := exec.LookPath("python"); err == nil {
			pythonBin = "python"
		} else if _, err := exec.LookPath("py"); err == nil {
			pythonBin = "py"
		} else {
			return fmt.Errorf("ACP bridge 需要 Python，请安装 Python 3")
		}
	}
	log.Printf("[ACP-DEBUG] using python: %s", pythonBin)

	cmd := exec.CommandContext(ctx, pythonBin, script,
		"--port", fmt.Sprintf("%d", acpPort),
		"--hostname", "127.0.0.1",
		"--runner", runner,
	)
	setCmdAttr(cmd)
	cmd.Stdout = logF
	cmd.Stderr = logF

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(logF, "START FAILED: %v\n", err)
		return fmt.Errorf("stdio_to_tcp.py start: %w", err)
	}

	fmt.Fprintf(logF, "PID=%d, waiting 500ms to check liveness...\n", cmd.Process.Pid)
	time.Sleep(500 * time.Millisecond)

	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		fmt.Fprintf(logF, "EXITED IMMEDIATELY code=%d\n", cmd.ProcessState.ExitCode())
		return fmt.Errorf("stdio_to_tcp.py exited immediately (code %d)", cmd.ProcessState.ExitCode())
	}

	a.acpCmd = cmd
	a.acpLogFile = logF
	log.Printf("ACP bridge started on :%d for %s (PID: %d)", acpPort, runner, cmd.Process.Pid)
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
	if a.acpLogFile != nil {
		a.acpLogFile.Close()
		a.acpLogFile = nil
	}
}

func (a *Agent) reconnectLoop(ctx context.Context, config *chclient.Config, serverURL, code string) {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		client, err := chclient.NewClient(config)
		if err != nil {
			log.Printf("Failed to create chisel client: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				backoff = minDuration(backoff*2, maxBackoff)
				continue
			}
		}

		err = client.Start(ctx)
		if err == nil {
			backoff = time.Second
			if serverURL != "" && code != "" {
				if reportErr := ReportConnected(serverURL, code); reportErr != nil {
					log.Printf("Failed to report connected: %v", reportErr)
				}
			}
			client.Wait()
		} else {
			log.Printf("Chisel connection error: %v", err)
		}

		select {
		case <-ctx.Done():
			client.Close()
			return
		default:
		}

		log.Printf("Tunnel disconnected, reconnecting in %v...", backoff)
		client.Close()

		a.setState("connecting", fmt.Sprintf("Reconnecting in %v...", backoff))

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			backoff = minDuration(backoff*2, maxBackoff)
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
