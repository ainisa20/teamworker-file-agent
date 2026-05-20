package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
	ID      any           `json:"id,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type MCPHandler struct {
	rootDir string
	token   string
}

func NewMCPHandler(root string, token string) *MCPHandler {
	return &MCPHandler{rootDir: root, token: token}
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.token != "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(authHeader, "Bearer ") || strings.TrimPrefix(authHeader, "Bearer ") != h.token {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	path := r.URL.Path
	if path == "" || path == "/" {
		path = "/mcp"
	}
	r.URL.Path = path

	if r.Method == http.MethodGet && (path == "/mcp" || path == "/health") {
		if path == "/health" {
			w.Write([]byte("OK\n"))
			return
		}
		h.handleInitialize(w, r)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.sendError(w, nil, -32700, "Parse error")
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.sendError(w, nil, -32700, "Parse error")
		return
	}

	switch req.Method {
	case "initialize":
		h.handleInitialize(w, r)
	case "notifications/initialized":
		h.sendResult(w, req.ID, map[string]any{})
	case "tools/list":
		h.handleListTools(w, req.ID)
	case "tools/call":
		h.handleCallTool(w, req)
	default:
		h.sendResult(w, req.ID, map[string]any{"tools": []any{}})
	}
}

func (h *MCPHandler) handleInitialize(w http.ResponseWriter, _ *http.Request) {
	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "file-agent",
			"version": "0.2.0",
		},
	}
	h.sendResult(w, nil, result)
}

func (h *MCPHandler) handleListTools(w http.ResponseWriter, id any) {
	tools := []Tool{
		{
			Name:        "read_file",
			Description: "Read the contents of a file",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Path relative to shared directory"},
				},
				"required": []any{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: "Write content to a file",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Path relative to shared directory"},
					"content": map[string]any{"type": "string", "description": "File content to write"},
				},
				"required": []any{"path", "content"},
			},
		},
		{
			Name:        "list_directory",
			Description: "List directory contents",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Path relative to shared directory (empty for root)"},
				},
			},
		},
		{
			Name:        "search_files",
			Description: "Search for files matching a pattern",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Base path relative to shared directory"},
					"pattern": map[string]any{"type": "string", "description": "Filename substring to match"},
				},
				"required": []any{"path", "pattern"},
			},
		},
	}
	h.sendResult(w, id, map[string]any{"tools": tools})
}

func (h *MCPHandler) handleCallTool(w http.ResponseWriter, req JSONRPCRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		h.sendError(w, req.ID, -32602, "Invalid params")
		return
	}

	var args map[string]any
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		args = map[string]any{}
	}

	var result any
	var errMsg string

	switch params.Name {
	case "read_file":
		result, errMsg = h.readFile(args)
	case "write_file":
		result, errMsg = h.writeFile(args)
	case "list_directory":
		result, errMsg = h.listDirectory(args)
	case "search_files":
		result, errMsg = h.searchFiles(args)
	default:
		errMsg = fmt.Sprintf("Unknown tool: %s", params.Name)
	}

	if errMsg != "" {
		h.sendError(w, req.ID, -32603, errMsg)
		return
	}

	h.sendResult(w, req.ID, map[string]any{"content": []any{result}})
}

func (h *MCPHandler) readFile(args map[string]any) (any, string) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, "Missing or invalid path"
	}

	fullPath := h.resolvePath(path)
	if fullPath == "" {
		return nil, "Access denied: path outside shared directory"
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "File not found"
		}
		return nil, fmt.Sprintf("Read error: %v", err)
	}

	return map[string]any{
		"type": "text",
		"text": string(data),
	}, ""
}

func (h *MCPHandler) writeFile(args map[string]any) (any, string) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, "Missing or invalid path"
	}

	fullPath := h.resolvePath(path)
	if fullPath == "" {
		return nil, "Access denied: path outside shared directory"
	}

	content, ok := args["content"].(string)
	if !ok {
		return nil, "Missing or invalid content"
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return nil, fmt.Sprintf("Write error: %v", err)
	}

	return map[string]any{
		"type": "text",
		"text": fmt.Sprintf("Written %d bytes to %s", len(content), path),
	}, ""
}

func (h *MCPHandler) listDirectory(args map[string]any) (any, string) {
	path, _ := args["path"].(string)

	fullPath := h.resolvePath(path)
	if fullPath == "" {
		return nil, "Access denied: path outside shared directory"
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "Directory not found"
		}
		return nil, fmt.Sprintf("Read error: %v", err)
	}

	var files []map[string]any
	for _, entry := range entries {
		info, _ := entry.Info()
		m := map[string]any{
			"name": entry.Name(),
			"type": map[bool]string{true: "directory", false: "file"}[entry.IsDir()],
		}
		if info != nil {
			m["size"] = info.Size()
		}
		files = append(files, m)
	}

	return map[string]any{
		"entries": files,
	}, ""
}

func (h *MCPHandler) searchFiles(args map[string]any) (any, string) {
	path, _ := args["path"].(string)
	pattern, _ := args["pattern"].(string)

	fullPath := h.resolvePath(path)
	if fullPath == "" {
		return nil, "Access denied: path outside shared directory"
	}

	var matches []string
	filepath.Walk(fullPath, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			name := info.Name()
			if pattern == "" || strings.Contains(name, pattern) {
				rel, _ := filepath.Rel(fullPath, p)
				matches = append(matches, rel)
			}
		}
		return nil
	})

	return map[string]any{
		"matches": matches,
	}, ""
}

func (h *MCPHandler) resolvePath(path string) string {
	if path == "" || path == "." {
		return h.rootDir
	}

	clean := filepath.Clean(filepath.Join(h.rootDir, path))
	if !strings.HasPrefix(clean, h.rootDir) {
		return ""
	}
	return clean
}

func (h *MCPHandler) sendResult(w http.ResponseWriter, id any, result any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      id,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *MCPHandler) sendError(w http.ResponseWriter, id any, code int, msg string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		Error: &JSONRPCError{
			Code:    code,
			Message: msg,
		},
		ID: id,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
