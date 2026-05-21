package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	chclient "github.com/jpillora/chisel/client"
	"github.com/teamworker/file-agent/internal/mcp"
)

var version = "0.3.0"

var defaultServerURL = ""

type ConnectConfig struct {
	Server   string `json:"server"`
	Port     int    `json:"port"`
	Auth     string `json:"auth"`
	User     string `json:"user"`
	MCPToken string `json:"mcp_token"`
	AgentID  string `json:"agent_id"`
	Code     string `json:"-"` // set locally, not from server
}

var connectionCodePattern = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)

func main() {
	serverAddr := flag.String("server", "", "Chisel server address (e.g., myserver.com:7000)")
	auth := flag.String("auth", "", "Chisel auth token")
	userID := flag.String("user", "", "Unique user ID for this connection")
	tunnelPort := flag.Int("tunnel-port", 0, "Remote port assigned by server for reverse tunnel (required)")
	localPort := flag.Int("local-port", 18080, "Local port for MCP server")
	mcpToken := flag.String("mcp-token", "", "Bearer token for MCP authentication (required)")
	serverURLFlag := flag.String("server-url", "", "TeamWorker server URL (e.g., https://47.239.24.30:8082)")
	keepAlive := flag.Duration("keepalive", 25*time.Second, "Keep-alive interval")
	verbose := flag.Bool("v", false, "Verbose logging")
	printVersion := flag.Bool("version", false, "Print version")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `file-agent v%s - Share local files via MCP over chisel tunnel

Usage:
  file-agent [connection-code] [shared-directory]
  file-agent [options] <shared-directory>

Quick Connect:
  file-agent F3K9-X2M7                        # Connect with code, share current directory
  file-agent F3K9-X2M7 /path/to/project       # Connect with code and directory

Interactive:
  file-agent                                   # Double-click or run with no args

Options:
`, version)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Environment Variables:
  TEAMWORKER_SERVER       Chisel server address
  TEAMWORKER_AUTH         Chisel auth token
  TEAMWORKER_USER         User ID
  TEAMWORKER_TUNNEL_PORT  Remote tunnel port
  TEAMWORKER_MCP_TOKEN    MCP Bearer token
  TEAMWORKER_SERVER_URL   TeamWorker server URL for connection codes

Legacy (full flags):
  file-agent --server myserver.com:7000 --auth mytoken --user alice --tunnel-port 9100 --mcp-token secret123 ./my-project
`)
	}

	flag.Parse()

	if *printVersion {
		fmt.Printf("file-agent version %s\n", version)
		os.Exit(0)
	}

	// Legacy mode: --server flag was explicitly provided
	if *serverAddr != "" {
		runLegacyMode(serverAddr, auth, userID, tunnelPort, localPort, mcpToken, keepAlive, verbose)
		return
	}

	runInteractiveMode(serverURLFlag, localPort, keepAlive, verbose)
}

func runLegacyMode(serverAddr, auth, userID *string, tunnelPort *int, localPort *int, mcpToken *string, keepAlive *time.Duration, verbose *bool) {
	server := *serverAddr
	if server == "" {
		server = os.Getenv("TEAMWORKER_SERVER")
		if server == "" {
			log.Fatalf("Server address required (--server or TEAMWORKER_SERVER)")
		}
	}

	authToken := *auth
	if authToken == "" {
		authToken = os.Getenv("TEAMWORKER_AUTH")
		if authToken == "" {
			log.Fatalf("Auth token required (--auth or TEAMWORKER_AUTH)")
		}
	}

	user := *userID
	if user == "" {
		user = os.Getenv("TEAMWORKER_USER")
		if user == "" {
			log.Fatalf("User ID required (--user or TEAMWORKER_USER)")
		}
	}

	tPort := *tunnelPort
	if tPort == 0 {
		tPortStr := os.Getenv("TEAMWORKER_TUNNEL_PORT")
		if tPortStr == "" {
			log.Fatalf("Tunnel port required (--tunnel-port or TEAMWORKER_TUNNEL_PORT)")
		}
		if _, err := fmt.Sscanf(tPortStr, "%d", &tPort); err != nil || tPort <= 0 {
			log.Fatalf("Invalid tunnel port: %s", tPortStr)
		}
	}

	mToken := *mcpToken
	if mToken == "" {
		mToken = os.Getenv("TEAMWORKER_MCP_TOKEN")
		if mToken == "" {
			log.Fatalf("MCP token required (--mcp-token or TEAMWORKER_MCP_TOKEN)")
		}
	}

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}

	startAgent(server, authToken, user, tPort, *localPort, mToken, dir, *keepAlive, *verbose, "", "")
}

func runInteractiveMode(serverURLFlag *string, localPort *int, keepAlive *time.Duration, verbose *bool) {
	fmt.Printf("\n📁 TeamWorker 文件共享客户端 v%s\n\n", version)

	serverURL := *serverURLFlag
	if serverURL == "" {
		serverURL = os.Getenv("TEAMWORKER_SERVER_URL")
	}
	if serverURL == "" {
		serverURL = defaultServerURL
	}
	if serverURL == "" {
		serverURL = promptInput("请输入服务器地址", "")
		if serverURL == "" {
			fmt.Println("✗ 服务器地址不能为空")
			waitOnWindows()
			os.Exit(1)
		}
	}

	// Determine connection code and directory from positional args
	var code, dirFromArg string
	args := flag.Args()

	if len(args) > 0 && connectionCodePattern.MatchString(args[0]) {
		code = args[0]
		if len(args) > 1 {
			dirFromArg = args[1]
		}
	} else if len(args) > 0 {
		dirFromArg = args[0]
	}

	if code == "" {
		code = promptInput("请输入连接码", "")
		if code == "" {
			fmt.Println("✗ 连接码不能为空")
			waitOnWindows()
			os.Exit(1)
		}
	}

	fmt.Print("✓ 正在获取连接配置... ")
	cfg, err := connectByCode(serverURL, code)
	if err != nil {
		fmt.Printf("\n✗ 获取连接配置失败: %v\n", err)
		waitOnWindows()
		os.Exit(1)
	}
	fmt.Println("成功")

	dir := dirFromArg
	if dir == "" {
		dir = promptInput("请输入共享目录", ".")
	}
	if dir == "" {
		dir = "."
	}

	fmt.Printf("✓ 正在连接 %s...\n", cfg.Server)
	cfg.Code = code
	startAgent(cfg.Server, cfg.Auth, cfg.User, cfg.Port, *localPort, cfg.MCPToken, dir, *keepAlive, *verbose, serverURL, code)
}

func connectByCode(serverURL, code string) (*ConnectConfig, error) {
	serverURL = strings.TrimRight(serverURL, "/")
	url := fmt.Sprintf("%s/api/tunnels/connect/%s", serverURL, code)

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: tr}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("无法连接服务器: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("服务器返回 %d: %s", resp.StatusCode, string(body))
	}

	var cfg ConnectConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	if cfg.Server == "" || cfg.Port == 0 || cfg.Auth == "" || cfg.User == "" || cfg.MCPToken == "" {
		return nil, fmt.Errorf("服务器返回的配置不完整")
	}

	return &cfg, nil
}

func reportConnected(serverURL, code string) {
	url := fmt.Sprintf("%s/api/tunnels/connect/%s/connected", strings.TrimRight(serverURL, "/"), code)
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		log.Printf("Failed to report connected: %v", err)
		return
	}
	resp.Body.Close()
}

func promptInput(prompt string, defaultValue string) string {
	if defaultValue != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultValue)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			return defaultValue
		}
		return input
	}
	return defaultValue
}

func startAgent(server, authToken, user string, tPort, localPort int, mToken, dir string, keepAlive time.Duration, verbose bool, serverURL, code string) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Printf("✗ 无法解析目录: %v\n", err)
		waitOnWindows()
		os.Exit(1)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("✗ 目录不存在: %s\n", absDir)
		} else {
			fmt.Printf("✗ 无法访问目录: %v\n", err)
		}
		waitOnWindows()
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Printf("✗ 不是目录: %s\n", absDir)
		waitOnWindows()
		os.Exit(1)
	}

	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)

	mcpHandler := mcp.NewMCPHandler(absDir, mToken)
	mcpServer := &http.Server{
		Addr:    localAddr,
		Handler: mcpHandler,
	}

	go func() {
		if err := mcpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("✗ MCP 服务器错误: %v\n", err)
			waitOnWindows()
			os.Exit(1)
		}
	}()

	remote := fmt.Sprintf("R:0.0.0.0:%d:127.0.0.1:%d", tPort, localPort)

	fmt.Println("✓ 隧道已建立！Agent 现在可以访问您的文件。")
	fmt.Printf("  共享目录: %s\n", absDir)
	fmt.Printf("  用户:     %s\n", user)
	fmt.Printf("  隧道:     R:127.0.0.1:%d → 127.0.0.1:%d\n", tPort, localPort)
	fmt.Println("  按 Ctrl+C 断开连接")

	chiselConfig := &chclient.Config{
		Server:    server,
		Auth:      fmt.Sprintf("%s:%s", user, authToken),
		KeepAlive: keepAlive,
		Remotes:   []string{remote},
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

func reconnectLoop(ctx context.Context, config *chclient.Config, serverURL, code string) {
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
				backoff = min(backoff*2, maxBackoff)
				continue
			}
		}

		err = client.Start(ctx)
		if err == nil {
			backoff = time.Second
			if serverURL != "" && code != "" {
				reportConnected(serverURL, code)
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

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			backoff = min(backoff*2, maxBackoff)
		}
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func mask(n int) string {
	if n <= 0 {
		return ""
	}
	mask := make([]byte, n)
	for i := range mask {
		mask[i] = '*'
	}
	return string(mask)
}

func waitOnWindows() {
	if runtime.GOOS == "windows" {
		fmt.Println("\n按回车键退出...")
		bufio.NewScanner(os.Stdin).Scan()
	}
}
