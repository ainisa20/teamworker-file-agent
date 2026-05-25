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
	"path/filepath"
	"strings"
	"sync"
	"time"

	chclient "github.com/jpillora/chisel/client"
	"github.com/teamworker/file-agent/internal/mcp"
)

// ConnectConfig holds the tunnel configuration returned by the server.
type ConnectConfig struct {
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Auth     string `json:"auth"`
	User     string `json:"user"`
	MCPToken string `json:"mcp_token"`
	AgentID  string `json:"agent_id"`
	Code     string `json:"-"` // set locally, not from server
}

// State represents the current agent state, exposed to the frontend via JSON.
type State struct {
	Status     string `json:"status"`      // disconnected, connecting, connected, error
	Message    string `json:"message"`     // human-readable status message
	ServerURL  string `json:"serverUrl"`   // the server URL being used
	Code       string `json:"code"`        // connection code
	SharedDir  string `json:"sharedDir"`   // absolute path to shared directory
	TunnelInfo string `json:"tunnelInfo"`  // e.g. "R:127.0.0.1:9100 → 127.0.0.1:18080"
}

// Agent holds all business logic for the file sharing agent.
type Agent struct {
	mu         sync.Mutex
	state      State
	cfg        *ConnectConfig
	serverURL  string

	// connection parameters
	localPort int
	keepAlive time.Duration
	verbose   bool

	// runtime
	mcpServer  *http.Server
	ctx        context.Context
	cancel     context.CancelFunc
}

// NewAgent creates a new Agent instance.
func NewAgent() *Agent {
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
	a.mu.Unlock()

	if cfg == nil {
		return fmt.Errorf("no connection config; call Connect first")
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

	// Start MCP server
	go func() {
		if err := a.mcpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("MCP server error: %v", err)
		}
	}()

	// Start chisel reconnect loop
	remote := fmt.Sprintf("R:0.0.0.0:%d:127.0.0.1:%d", cfg.Port, a.localPort)
	chiselConfig := &chclient.Config{
		Server:    cfg.Server,
		Auth:      fmt.Sprintf("%s:%s", cfg.User, cfg.Auth),
		KeepAlive: a.keepAlive,
		Remotes:   []string{remote},
		Verbose:   a.verbose,
	}

	go a.reconnectLoop(ctx, chiselConfig, serverURL, cfg.Code)

	a.mu.Lock()
	a.state.Status = "connected"
	a.state.Message = "Tunnel established"
	a.state.TunnelInfo = fmt.Sprintf("R:127.0.0.1:%d → 127.0.0.1:%d", cfg.Port, a.localPort)
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

	a.state = State{
		Status: "disconnected",
	}
}

// reconnectLoop manages the chisel client with exponential backoff.
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
