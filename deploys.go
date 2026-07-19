package main

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Deploy history: every deploy writes its own log file under
// deploylog/<app>/<id>.log with a header (start time, source) and a footer
// (ok or failed, end time). The history list is derived from those files,
// so there is no separate index to keep in sync.

const deployKeep = 20

var deployIDRe = regexp.MustCompile(`^\d{8}-\d{6}$`)

func deployDir(app string) string { return filepath.Join(dataDir, "deploylog", app) }

type deployEntry struct {
	ID       string `json:"id"`
	Started  string `json:"started"`
	Finished string `json:"finished,omitempty"`
	Source   string `json:"source,omitempty"`
	Status   string `json:"status"` // success | failed | running | interrupted
}

func parseDeployLog(app, id string) deployEntry {
	e := deployEntry{ID: id, Status: "interrupted"}
	b, err := os.ReadFile(filepath.Join(deployDir(app), id+".log"))
	if err != nil {
		return e
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i, l := range lines {
		if i > 2 {
			break
		}
		if s, ok := strings.CutPrefix(l, "=== deploy started "); ok {
			e.Started = s
		}
		if s, ok := strings.CutPrefix(l, "=== source "); ok {
			e.Source = s
		}
	}
	last := lines[len(lines)-1]
	if s, ok := strings.CutPrefix(last, "=== deploy finished ok "); ok {
		e.Finished, e.Status = s, "success"
	} else if s, ok := strings.CutPrefix(last, "=== deploy finished failed "); ok {
		e.Finished, e.Status = s, "failed"
	} else if busyWith() == "deploy" {
		e.Status = "running"
	}
	return e
}

func deployIDs(app string) []string {
	entries, err := os.ReadDir(deployDir(app))
	if err != nil {
		return nil
	}
	ids := []string{}
	for _, f := range entries {
		id := strings.TrimSuffix(f.Name(), ".log")
		if deployIDRe.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids))) // ids are timestamps, newest first
	return ids
}

// pruneDeployLogs keeps the newest deployKeep-1 logs so a new one fits.
func pruneDeployLogs(app string) {
	ids := deployIDs(app)
	for i, id := range ids {
		if i >= deployKeep-1 {
			os.Remove(filepath.Join(deployDir(app), id+".log"))
		}
	}
}

func handleDeploys(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	out := []deployEntry{}
	for _, id := range deployIDs(name) {
		out = append(out, parseDeployLog(name, id))
	}
	writeJSON(w, out)
}
