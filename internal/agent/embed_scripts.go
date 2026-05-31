package agent

import (
	"os"
	"path/filepath"
	"sync"

	_ "embed"
)

//go:embed scripts/stdio_to_tcp.py
var stdioToTCPScript []byte

var (
	embeddedScriptPath string
	embeddedOnce       sync.Once
	embeddedErr        error
)

// EnsureACPBridge extracts the embedded stdio_to_tcp.py to a temp directory
// and returns its path. It is safe to call concurrently; extraction happens once.
func EnsureACPBridge() (string, error) {
	embeddedOnce.Do(func() {
		dir := os.TempDir()
		path := filepath.Join(dir, "file-agent-stdio_to_tcp.py")
		embeddedErr = os.WriteFile(path, stdioToTCPScript, 0644)
		if embeddedErr == nil {
			embeddedScriptPath = path
		}
	})
	if embeddedErr != nil {
		return "", embeddedErr
	}
	return embeddedScriptPath, nil
}
