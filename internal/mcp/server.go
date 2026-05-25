package mcp

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
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
	rootDir   string
	token     string
	tunnelURL string
	serverURL string
	code      string
}

func NewMCPHandler(root string, token string, tunnelURL string, serverURL string, code string) *MCPHandler {
	return &MCPHandler{rootDir: root, token: token, tunnelURL: tunnelURL, serverURL: serverURL, code: code}
}

func (h *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "" || path == "/" {
		path = "/mcp"
	}

	// File proxy: serve binary files without Bearer auth
	// qwenpaw's view_image downloads via plain HTTP GET
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/files/") {
		h.serveFile(w, r)
		return
	}

	if h.token != "" {
		authHeader := r.Header.Get("Authorization")
		log.Printf("DEBUG MCP auth: token='%s', header='%s'", h.token, authHeader)
		if authHeader == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(authHeader, "Bearer ") || strings.TrimPrefix(authHeader, "Bearer ") != h.token {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
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
			Name:        "local_read_file",
			Description: "Read the contents of a file on the user's local computer",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Path relative to shared directory"},
				},
				"required": []any{"path"},
			},
		},
		{
			Name:        "local_write_file",
			Description: "Write content to a file on the user's local computer",
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
			Name:        "local_list_directory",
			Description: "List directory contents on the user's local computer",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Path relative to shared directory (empty for root)"},
				},
			},
		},
		{
			Name:        "local_search_files",
			Description: "Search for files matching a pattern on the user's local computer (recursive)",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":          map[string]any{"type": "string", "description": "Base path relative to shared directory (empty for root)"},
					"pattern":       map[string]any{"type": "string", "description": "Filename pattern to match (supports wildcards: *.go, test_*.py)"},
					"max_depth":     map[string]any{"type": "integer", "description": "Maximum directory depth to search (default: 10, 0=unlimited)"},
					"max_results":   map[string]any{"type": "integer", "description": "Maximum number of results to return (default: 100, 0=unlimited)"},
					"exclude_dirs":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Directory names to exclude (default: [node_modules, .git, target, dist, build])"},
					"case_sensitive": map[string]any{"type": "boolean", "description": "Case-sensitive matching (default: false)"},
				},
				"required": []any{"pattern"},
			},
		},
		{
			Name:        "local_view_image",
			Description: "Analyze the content of an image file on the user's local computer using a vision model. Returns a text description of the image (what's in the image, any text, charts, etc.). Use this instead of local_read_file when you need to understand image content.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Image file path relative to shared directory (.png, .jpg, .jpeg, .gif, .webp, .bmp)"},
				},
				"required": []any{"path"},
			},
		},
		{
			Name:        "local_upload_file",
			Description: "Upload a file from the user's local computer to an agent's workspace on the server. The file will be accessible to the agent via a URL. Use this to share local files (documents, images, data files, archives) that the agent needs to process.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":     map[string]any{"type": "string", "description": "Local file path relative to shared directory"},
					"agent_id": map[string]any{"type": "string", "description": "Target agent ID (pass the agent's own ID)"},
				},
				"required": []any{"path", "agent_id"},
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
	case "local_read_file":
		result, errMsg = h.readFile(args)
	case "local_write_file":
		result, errMsg = h.writeFile(args)
	case "local_list_directory":
		result, errMsg = h.listDirectory(args)
	case "local_search_files":
		result, errMsg = h.searchFiles(args)
	case "local_view_image":
		result, errMsg = h.viewImage(args)
	case "local_upload_file":
		result, errMsg = h.uploadFile(args)
	default:
		errMsg = fmt.Sprintf("Unknown tool: %s", params.Name)
	}

	if errMsg != "" {
		h.sendError(w, req.ID, -32603, errMsg)
		return
	}

	var textContent string
	if result != nil {
		if m, ok := result.(map[string]any); ok {
			if val, ok := m["text"]; ok && val != nil {
				textContent, _ = val.(string)
			}
		}
	}
	// Ensure textContent is never nil/None for pydantic validation
	if textContent == "" {
		textContent = "(empty result)"
	}
	h.sendResult(w, req.ID, map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": textContent,
			},
		},
	})
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

	ext := strings.ToLower(filepath.Ext(fullPath))
	fileName := filepath.Base(fullPath)

	if isImageFile(fullPath) {
		relPath := strings.TrimPrefix(fullPath, h.rootDir)
		relPath = strings.TrimPrefix(relPath, string(filepath.Separator))
		url := fmt.Sprintf("%s/files/%s", h.tunnelURL, filepath.ToSlash(relPath))
		return map[string]any{
			"text": fmt.Sprintf("[Image file: %s]\nSize: %d bytes\nURL: %s\n\nUse view_image tool with the URL above to load this image into the conversation.", fileName, len(data), url),
		}, ""
	}

	if ext == ".xlsx" || ext == ".xls" {
		text, parseErr := parseXlsx(data, fileName)
		if parseErr != nil {
			return map[string]any{
				"text": fmt.Sprintf("[Excel file: %s] Size: %d bytes\n\nCould not parse file: %v\nThe file may be encrypted or in an unsupported format.", fileName, len(data), parseErr),
			}, ""
		}
		return map[string]any{"text": text}, ""
	}

	if ext == ".pdf" {
		text, parseErr := parsePdf(data, fileName)
		if parseErr != nil {
			return map[string]any{
				"text": fmt.Sprintf("[PDF file: %s] Size: %d bytes\n\nCould not extract text: %v\nThe PDF may be image-based or encrypted.", fileName, len(data), parseErr),
			}, ""
		}
		return map[string]any{"text": text}, ""
	}

	if ext == ".csv" {
		return map[string]any{"text": string(data)}, ""
	}

	if isBinary(data) {
		return map[string]any{
			"text": fmt.Sprintf("[Binary file: %s]\nSize: %d bytes\n\nThis is a binary file that cannot be displayed as text.", fileName, len(data)),
		}, ""
	}

	return map[string]any{
		"text": string(data),
	}, ""
}

func isBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	// Check first 8KB for null bytes and non-text bytes
	sampleSize := 8192
	if len(data) < sampleSize {
		sampleSize = len(data)
	}
	sample := data[:sampleSize]

	// Count null bytes and non-printable bytes
	nullCount := 0
	nonPrintableCount := 0
	for _, b := range sample {
		if b == 0 {
			nullCount++
		} else if b < 32 && b != 9 && b != 10 && b != 13 {
			// Exclude tab, newline, carriage return
			nonPrintableCount++
		}
	}

	// If more than 1% null bytes or 5% non-printable bytes, consider it binary
	return nullCount > len(sample)/100 || nonPrintableCount > len(sample)/20
}

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true, ".ico": true,
}

var mediaExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true,
	".mp4": true, ".webm": true, ".mpeg": true, ".mov": true,
	".avi": true, ".mkv": true,
}

func isImageFile(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

func isMediaFile(path string) bool {
	return mediaExtensions[strings.ToLower(filepath.Ext(path))]
}

func (h *MCPHandler) serveFile(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/files/")
	if relPath == "" {
		http.Error(w, "File path required", http.StatusBadRequest)
		return
	}

	fullPath := h.resolvePath(relPath)
	if fullPath == "" {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "File not found", http.StatusNotFound)
		} else {
			http.Error(w, "Access error", http.StatusInternalServerError)
		}
		return
	}

	if info.IsDir() {
		http.Error(w, "Not a file", http.StatusBadRequest)
		return
	}

	// 50MB limit for file serving
	if info.Size() > 50*1024*1024 {
		http.Error(w, "File too large (max 50MB)", http.StatusRequestEntityTooLarge)
		return
	}

	mimeType := mime.TypeByExtension(filepath.Ext(fullPath))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Cache-Control", "no-cache")

	f, err := os.Open(fullPath)
	if err != nil {
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	io.Copy(w, f)
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
		"text": formatDirEntries(files),
	}, ""
}

func (h *MCPHandler) searchFiles(args map[string]any) (any, string) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return nil, "Pattern is required"
	}

	path, _ := args["path"].(string)
	maxDepth := 10
	if md, ok := args["max_depth"].(float64); ok {
		maxDepth = int(md)
	}
	maxResults := 100
	if mr, ok := args["max_results"].(float64); ok {
		maxResults = int(mr)
	}

	excludeDirs := map[string]bool{
		"node_modules": true,
		".git":         true,
		"target":       true,
		"dist":         true,
		"build":        true,
		"bin":          true,
		"obj":          true,
		".vscode":      true,
		".idea":        true,
	}
	if ed, ok := args["exclude_dirs"].([]any); ok {
		for _, d := range ed {
			if dir, ok := d.(string); ok {
				excludeDirs[dir] = true
			}
		}
	}

	caseSensitive := false
	if cs, ok := args["case_sensitive"].(bool); ok {
		caseSensitive = cs
	}

	searchPattern := pattern
	if !caseSensitive {
		searchPattern = strings.ToLower(pattern)
	}

	fullPath := h.resolvePath(path)
	if fullPath == "" {
		return nil, "Access denied: path outside shared directory"
	}

	type Match struct {
		Path string
		Size int64
	}
	var matches []Match
	var walkErr error

	filepath.Walk(fullPath, func(p string, info os.FileInfo, err error) error {
		if walkErr != nil {
			return walkErr
		}

		if err != nil {
			return nil
		}

		relPath, err := filepath.Rel(fullPath, p)
		if err != nil {
			return nil
		}

		if info.IsDir() {
			if excludeDirs[info.Name()] {
				return filepath.SkipDir
			}

			if maxDepth > 0 {
				depth := len(strings.Split(filepath.ToSlash(relPath), "/"))
				if depth > maxDepth {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if maxResults > 0 && len(matches) >= maxResults {
			walkErr = fmt.Errorf("reached maximum results limit (%d)", maxResults)
			return walkErr
		}

		name := info.Name()
		matchName := name
		if !caseSensitive {
			matchName = strings.ToLower(name)
		}

		matched, _ := filepath.Match(searchPattern, matchName)
		if !matched {
			matched = strings.Contains(matchName, searchPattern)
		}

		if matched {
			matches = append(matches, Match{
				Path: relPath,
				Size: info.Size(),
			})
		}

		return nil
	})

	var lines []string
	for _, m := range matches {
		lines = append(lines, fmt.Sprintf("  %s (%d bytes)", m.Path, m.Size))
	}

	resultMsg := fmt.Sprintf("Found %d matching files", len(matches))
	if maxResults > 0 && len(matches) >= maxResults {
		resultMsg += fmt.Sprintf(" (limited to %d results)", maxResults)
	}
	if walkErr != nil {
		resultMsg += fmt.Sprintf("\nSearch stopped: %v", walkErr)
	}

	return map[string]any{
		"text": fmt.Sprintf("%s:\n%s", resultMsg, strings.Join(lines, "\n")),
	}, ""
}

func (h *MCPHandler) viewImage(args map[string]any) (any, string) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, "Missing or invalid path"
	}
	fullPath := h.resolvePath(path)
	if fullPath == "" {
		return nil, "Access denied: path outside shared directory"
	}
	if !isImageFile(fullPath) {
		return nil, "Not an image file. Use local_read_file for non-image files."
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "File not found"
		}
		return nil, fmt.Sprintf("Read error: %v", err)
	}
	if len(data) > 10*1024*1024 {
		return nil, fmt.Sprintf("Image too large: %d bytes (max 10MB for vision analysis)", len(data))
	}
	if h.serverURL == "" || h.code == "" {
		return nil, "Vision analysis unavailable: not connected via teamworker server"
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	reqBody, _ := json.Marshal(map[string]string{
		"code":         h.code,
		"mcp_token":    h.token,
		"filename":     filepath.Base(fullPath),
		"image_base64": encoded,
	})

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Timeout: 120 * time.Second, Transport: tr}
	url := strings.TrimRight(h.serverURL, "/") + "/api/user-tunnels/analyze-image"
	resp, err := client.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Sprintf("Vision API request failed: %v. Please check teamworker server connectivity.", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 429 {
		return nil, "Rate limit exceeded: please wait before analyzing more images (max 10 per minute)"
	}
	if resp.StatusCode == 401 {
		return nil, "Vision API authentication failed: tunnel may be stale, reconnect required"
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Sprintf("Vision API returned %d: %s. Vision model may be unavailable, please notify the user and do not retry.", resp.StatusCode, string(body))
	}
	var result struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Sprintf("Parse error: %v", err)
	}
	return map[string]any{
		"text": fmt.Sprintf("[Image analysis: %s]\n%s", filepath.Base(fullPath), result.Description),
	}, ""
}

func (h *MCPHandler) uploadFile(args map[string]any) (any, string) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, "Missing or invalid path"
	}
	agentID, ok := args["agent_id"].(string)
	if !ok || agentID == "" {
		return nil, "Missing or invalid agent_id"
	}

	fullPath := h.resolvePath(path)
	if fullPath == "" {
		return nil, "Access denied: path outside shared directory"
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "File not found"
		}
		return nil, fmt.Sprintf("Stat error: %v", err)
	}
	if info.IsDir() {
		return nil, "Path is a directory, not a file"
	}
	if info.Size() > 50*1024*1024 {
		return nil, fmt.Sprintf("File too large: %d bytes (max 50MB)", info.Size())
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Sprintf("Read error: %v", err)
	}

	if h.serverURL == "" || h.code == "" {
		return nil, "Upload unavailable: not connected via teamworker server"
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	reqBody, _ := json.Marshal(map[string]string{
		"code":        h.code,
		"mcp_token":   h.token,
		"agent_id":    agentID,
		"filename":    filepath.Base(fullPath),
		"content_b64": encoded,
	})

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Timeout: 120 * time.Second, Transport: tr}
	url := strings.TrimRight(h.serverURL, "/") + "/api/user-tunnels/upload-file"
	resp, err := client.Post(url, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Sprintf("Upload API request failed: %v. Please check teamworker server connectivity.", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 429 {
		return nil, "Rate limit exceeded: please wait before uploading more files"
	}
	if resp.StatusCode == 401 {
		return nil, "Upload API authentication failed: tunnel may be stale, reconnect required"
	}
	if resp.StatusCode == 413 {
		return nil, "File too large for server: exceeds server upload limit"
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Sprintf("Upload API returned %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Success  bool   `json:"success"`
		Filename string `json:"filename"`
		FileURL  string `json:"file_url"`
		Size     int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Sprintf("Parse error: %v", err)
	}
	return map[string]any{
		"text": fmt.Sprintf("Uploaded %s (%d bytes) to agent %s workspace. URL: %s", result.Filename, result.Size, agentID, result.FileURL),
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

func formatDirEntries(files []map[string]any) string {
	var lines []string
	for _, f := range files {
		t := f["type"]
		n := f["name"]
		s := f["size"]
		if t == "directory" {
			lines = append(lines, fmt.Sprintf("  📁 %s/", n))
		} else {
			if s != nil {
				lines = append(lines, fmt.Sprintf("  📄 %s (%d bytes)", n, s))
			} else {
				lines = append(lines, fmt.Sprintf("  📄 %s", n))
			}
		}
	}
	if len(lines) == 0 {
		return "(empty directory)"
	}
	return strings.Join(lines, "\n")
}

func parseXlsx(data []byte, fileName string) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to open xlsx: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return fmt.Sprintf("[Excel file: %s] No sheets found", fileName), nil
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("[Excel file: %s] %d sheet(s)\n\n", fileName, len(sheets)))

	maxRowsPerSheet := 200
	maxCols := 20

	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			buf.WriteString(fmt.Sprintf("=== Sheet: %s ===\nError: %v\n\n", sheet, err))
			continue
		}

		totalRows := len(rows)
		buf.WriteString(fmt.Sprintf("=== Sheet: %s (%d rows) ===\n", sheet, totalRows))

		rowLimit := totalRows
		if rowLimit > maxRowsPerSheet {
			rowLimit = maxRowsPerSheet
		}

		for i, row := range rows {
			if i >= rowLimit {
				buf.WriteString(fmt.Sprintf("... (%d more rows)\n", totalRows-rowLimit))
				break
			}
			cells := row
			if len(cells) > maxCols {
				cells = cells[:maxCols]
			}
			buf.WriteString(strings.Join(cells, " | "))
			buf.WriteString("\n")
		}
		buf.WriteString("\n")
	}

	return buf.String(), nil
}

func parsePdf(data []byte, fileName string) (string, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to open pdf: %w", err)
	}

	totalPages := reader.NumPage()
	if totalPages == 0 {
		return fmt.Sprintf("[PDF file: %s] No pages found", fileName), nil
	}

	maxPages := 50
	pageLimit := totalPages
	if pageLimit > maxPages {
		pageLimit = maxPages
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("[PDF file: %s] %d page(s)\n\n", fileName, totalPages))

	for i := 1; i <= pageLimit; i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			buf.WriteString(fmt.Sprintf("--- Page %d ---\n(text extraction failed)\n\n", i))
			continue
		}
		text = strings.TrimSpace(text)
		if text != "" {
			buf.WriteString(fmt.Sprintf("--- Page %d ---\n%s\n\n", i, text))
		}
	}

	if pageLimit < totalPages {
		buf.WriteString(fmt.Sprintf("... (%d more pages)\n", totalPages-pageLimit))
	}

	result := buf.String()
	if len(result) > 100*1024 {
		result = result[:100*1024] + "\n\n[Text truncated at 100KB]"
	}

	return result, nil
}

func (h *MCPHandler) sendResult(w http.ResponseWriter, id any, result any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      id,
	}
	if id == nil {
		resp.ID = 0
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *MCPHandler) sendError(w http.ResponseWriter, id any, code int, msg string) {
	eid := id
	if eid == nil {
		eid = 0
	}
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		Error: &JSONRPCError{
			Code:    code,
			Message: msg,
		},
		ID: eid,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
