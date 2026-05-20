# file-agent v0.2.0

Share local files via MCP (Model Context Protocol) over a chisel reverse tunnel.

## Quick Start

1. Get your connection command from the TeamWorker UI (Agent List → 本地连接)
2. Download the binary for your platform from `dist/`
3. Run the command:

```bash
./file-agent --server 47.239.24.30:7000 --auth <token> --user <user_id> --tunnel-port 9100 --mcp-token <mcp_token> ./my-project
```

## Architecture

```
Your Computer                           Server (Docker Network)
┌──────────────────────┐               ┌─────────────────────────────┐
│ file-agent           │               │ chisel-server :7000         │
│  ├─ MCP server       │◄── reverse ──│  └─ :9100 (user A tunnel)  │
│  │  :18080           │    tunnel     │  └─ :9101 (user B tunnel)  │
│  └─ chisel client    │               │                             │
│     → ws://server     │               │ team_worker → chisel:9100  │
└──────────────────────┘               │   → file-agent MCP server   │
                                       │                             │
                                       │ qwenpaw → team_worker      │
                                       │   → agent reads your files │
                                       └─────────────────────────────┘
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
  file-agent [options] <shared-directory>

Options:
  --server string        Chisel server address (or TEAMWORKER_SERVER)
  --auth string          Chisel auth token (or TEAMWORKER_AUTH)
  --user string          User ID (or TEAMWORKER_USER)
  --tunnel-port int      Remote port assigned by server (or TEAMWORKER_TUNNEL_PORT)
  --local-port int       Local port for MCP server (default 18080)
  --mcp-token string     MCP Bearer token (or TEAMWORKER_MCP_TOKEN)
  --keepalive duration   Keep-alive interval (default 25s)
  -v                     Verbose logging
  --version              Print version
```

## Build from Source

```bash
go build -o file-agent ./cmd/file-agent
```

Cross-compile:
```bash
mkdir -p dist
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/file-agent-darwin-arm64 ./cmd/file-agent
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o dist/file-agent-darwin-amd64 ./cmd/file-agent
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/file-agent-linux-amd64 ./cmd/file-agent
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/file-agent-windows-amd64.exe ./cmd/file-agent
```

## MCP Tools

- `read_file` - Read file contents (path relative to shared directory)
- `write_file` - Write content to a file
- `list_directory` - List directory contents
- `search_files` - Search for files by name pattern
