package main

// Append-only audit trail of every state-changing panel action (non-GET),
// with who did it: the admin session or a named API token.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var auditMu sync.Mutex

func auditPath() string { return filepath.Join(dataDir, "audit.log") }

func audit(r *http.Request, actor string) {
	if r.Method == http.MethodGet {
		return
	}
	auditMu.Lock()
	defer auditMu.Unlock()
	f, err := os.OpenFile(auditPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%s\t%s %s\n", time.Now().Format(time.RFC3339), clientIP(r), actor, r.Method, r.URL.Path)
}

func handleAudit(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(auditPath())
	if err != nil {
		writeJSON(w, map[string]any{"lines": []string{}})
		return
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	// newest first
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	writeJSON(w, map[string]any{"lines": lines})
}
