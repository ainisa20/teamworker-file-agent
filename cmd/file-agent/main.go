package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	chclient "github.com/jpillora/chisel/client"
	"github.com/teamworker/file-agent/internal/mcp"
)

var version = "0.2.0"

func main() {
	serverAddr := flag.String("server", "", "Chisel server address (e.g., myserver.com:7000)")
	auth := flag.String("auth", "", "Chisel auth token")
	userID := flag.String("user", "", "Unique user ID for this connection")
	tunnelPort := flag.Int("tunnel-port", 0, "Remote port assigned by server for reverse tunnel (required)")
	localPort := flag.Int("local-port", 18080, "Local port for MCP server")
	mcpToken := flag.String("mcp-token", "", "Bearer token for MCP authentication (required)")
	keepAlive := flag.Duration("keepalive", 25*time.Second, "Keep-alive interval")
	verbose := flag.Bool("v", false, "Verbose logging")
	printVersion := flag.Bool("version", false, "Print version")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `file-agent v%s - Share local files via MCP over chisel tunnel

Usage:
  file-agent [options] <shared-directory>

Options:
`, version)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  file-agent --server myserver.com:7000 --auth mytoken --user alice --tunnel-port 9100 --mcp-token secret123 ./my-project

Environment Variables:
  TEAMWORKER_SERVER   Chisel server address
  TEAMWORKER_AUTH     Chisel auth token
  TEAMWORKER_USER     User ID
  TEAMWORKER_TUNNEL_PORT  Remote tunnel port
  TEAMWORKER_MCP_TOKEN    MCP Bearer token
`, version)
	}

	flag.Parse()

	if *printVersion {
		fmt.Printf("file-agent version %s\n", version)
		os.Exit(0)
	}

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		log.Fatalf("Failed to resolve directory: %v", err)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatalf("Directory does not exist: %s", absDir)
		}
		log.Fatalf("Failed to stat directory: %v", err)
	}
	if !info.IsDir() {
		log.Fatalf("Not a directory: %s", absDir)
	}

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

	localAddr := fmt.Sprintf("127.0.0.1:%d", *localPort)

	mcpHandler := mcp.NewMCPHandler(absDir, mToken)
	mcpServer := &http.Server{
		Addr:    localAddr,
		Handler: mcpHandler,
	}

	go func() {
		fmt.Printf("Starting MCP server on %s\n", localAddr)
		if err := mcpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("MCP server error: %v", err)
		}
	}()

	// R:127.0.0.1:{tunnelPort}:127.0.0.1:{localPort}
	// Binds reverse tunnel to 127.0.0.1 only on the server side (not 0.0.0.0)
	remote := fmt.Sprintf("R:127.0.0.1:%d:127.0.0.1:%d", tPort, *localPort)

	fmt.Printf("Connecting to %s...\n", server)
	fmt.Printf("Sharing:    %s\n", absDir)
	fmt.Printf("User:       %s\n", user)
	fmt.Printf("Tunnel:     R:127.0.0.1:%d → 127.0.0.1:%d\n", tPort, *localPort)
	fmt.Printf("MCP token:  %s...%s\n", mToken[:minInt(4, len(mToken))], mask(len(mToken)-4))

	chiselConfig := &chclient.Config{
		Server:    server,
		Auth:      fmt.Sprintf("%s:%s", user, authToken),
		KeepAlive: *keepAlive,
		Remotes:   []string{remote},
		Verbose:   *verbose,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go reconnectLoop(ctx, chiselConfig)

	<-sigCh
	fmt.Println("\nShutting down...")
	cancel()
	mcpServer.Close()
}

func reconnectLoop(ctx context.Context, config *chclient.Config) {
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
			fmt.Println("Tunnel established!")
			backoff = time.Second
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
