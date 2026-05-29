# file-agent v0.8.0

Share local files via MCP + remote code execution via ACP over chisel reverse tunnels.

## Quick Start

1. Get a connection code from the TeamWorker UI
2. Download the binary for your platform from `dist/`
3. Run with connection code:

```bash
./file-agent F3K9-X2M7
./file-agent F3K9-X2M7 /path/to/project
```

Or double-click the binary and enter the code interactively:

```
$ ./file-agent-darwin-arm64

📁 TeamWorker 文件共享客户端 v0.8.0

请输入连接码: F3K9-X2M7
请输入共享目录 [当前目录 .]: /Users/zhangsan/project

✓ 已获取连接配置
✓ 正在连接 47.239.24.30:7000...
✓ 隧道已建立！Agent 现在可以访问您的文件。
  共享目录: /Users/zhangsan/project
  按 Ctrl+C 断开连接
```

## Desktop App (macOS)

`file-agent-macOS-arm64.zip` 包含 Wails 桌面应用，支持：

- 连接码 + 共享目录选择
- 外部智能体检测与一键安装（OpenCode / Claude Code / Codex / Qwen Code）
- npm 环境检测与安装引导
- 交互式 PTY 终端（xterm.js）
- MCP 文件共享 + ACP 代码执行双隧道

## Architecture

```
用户本地 Mac (file-agent):
┌─────────────────────────────────────────────────┐
│  MCP server :18080 (文件共享)                     │
│  ACP bridge :4096  (代码执行, stdio_to_tcp.py)    │
│  Chisel client → server:7000                     │
│    R:0.0.0.0:9101 → :18080 (MCP)                 │
│    R:0.0.0.0:9102 → :4096  (ACP)                 │
└─────────────────────────────────────────────────┘

服务器 Docker 网络:
┌─────────────────────────────────────────────────┐
│  chisel-server :7000                             │
│    :9101 → userA MCP   :9102 → userA ACP         │
│    :9103 → userB MCP   :9104 → userB ACP         │
│                                                   │
│  acp-mux :4099                                   │
│    RUNNER:opencode → chisel-server:9102           │
│                                                   │
│  qwenpaw                                         │
│    → acp-mux:4099 → chisel:9102 → 用户本地 opencode│
└─────────────────────────────────────────────────┘
```

### Security

- **Reverse tunnel binds to 127.0.0.1 only** on the server (not 0.0.0.0)
- **Docker network isolation**: tunnel ports only accessible within the Docker network
- **Per-user chisel auth** + ACL: each user can only bind their assigned port
- **MCP Bearer token**: every MCP request requires authentication
- **Path traversal protection**: file access restricted to the shared directory

## CLI Options

```
Usage:
  file-agent [connection-code] [shared-directory]
  file-agent [options] <shared-directory>

Quick Connect:
  file-agent F3K9-X2M7                        # Connect with code, share current directory
  file-agent F3K9-X2M7 /path/to/project       # Connect with code and directory

Interactive:
  file-agent                                   # Double-click or run with no args

Options:
  --server-url string   TeamWorker server URL (or TEAMWORKER_SERVER_URL)
  --server string       Chisel server address (or TEAMWORKER_SERVER)
  --auth string         Chisel auth token (or TEAMWORKER_AUTH)
  --user string         User ID (or TEAMWORKER_USER)
  --tunnel-port int     Remote port assigned by server (or TEAMWORKER_TUNNEL_PORT)
  --local-port int      Local port for MCP server (default 18080)
  --mcp-token string    MCP Bearer token (or TEAMWORKER_MCP_TOKEN)
  --keepalive duration  Keep-alive interval (default 25s)
  -v                    Verbose logging
  --version             Print version
```

## Build from Source

```bash
# CLI
go build -o file-agent ./cmd/file-agent

# Wails 桌面版 (macOS)
wails build -platform darwin/universal -ldflags "-s -w"
```

Cross-compile CLI with embedded server URL:
```bash
SERVER_URL="https://47.239.24.30:8082"
mkdir -p dist
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X main.defaultServerURL=${SERVER_URL}" -o dist/file-agent-darwin-arm64 ./cmd/file-agent
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X main.defaultServerURL=${SERVER_URL}" -o dist/file-agent-darwin-amd64 ./cmd/file-agent
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.defaultServerURL=${SERVER_URL}" -o dist/file-agent-linux-amd64 ./cmd/file-agent
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X main.defaultServerURL=${SERVER_URL}" -o dist/file-agent-windows-amd64.exe ./cmd/file-agent
```

## MCP Tools

- `read_file` - Read file contents (path relative to shared directory)
- `write_file` - Write content to a file
- `list_directory` - List directory contents
- `search_files` - Search for files by name pattern
- `upload_file` - Upload a local file to an agent's workspace on the server
