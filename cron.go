package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Panel-managed cron: jobs live in meta.json (source of truth); each save
// regenerates /etc/cron.d/gantry-<app>. Each job's command runs in a fresh
// one-off container (dokku --rm run) and appends "<timestamp> exit=<code>"
// to a per-job log the panel reads back as "last run".

var cronDir = env("GANTRY_CRON_DIR", "/etc/cron.d")

func cronLogDir() string { return filepath.Join(dataDir, "cronlog") }

type cronJob struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Last     string `json:"last,omitempty"` // filled on read, not stored
}

type appMeta struct {
	Category   string    `json:"category,omitempty"`
	Jobs       []cronJob `json:"jobs,omitempty"`
	Repo       string    `json:"repo,omitempty"`
	Ref        string    `json:"ref,omitempty"`
	BuildDir   string    `json:"build_dir,omitempty"`
	Dockerfile string    `json:"dockerfile,omitempty"`
	Image      string    `json:"image,omitempty"`

	LastDeploy   string `json:"last_deploy,omitempty"` // RFC3339
	LastDeployOK bool   `json:"last_deploy_ok,omitempty"`

	MaintenanceTpl string `json:"maintenance_tpl,omitempty"` // last-used page template
}

var (
	metaMu sync.Mutex
	meta   = map[string]*appMeta{}
)

func metaPath() string { return filepath.Join(dataDir, "meta.json") }

func loadMeta() {
	if b, err := os.ReadFile(metaPath()); err == nil {
		json.Unmarshal(b, &meta)
	}
}

func saveMeta() error { // callers hold metaMu
	os.MkdirAll(dataDir, 0o755)
	b, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(metaPath(), b, 0o644)
}

func getMeta(app string) *appMeta { // callers hold metaMu
	m := meta[app]
	if m == nil {
		m = &appMeta{}
		meta[app] = m
	}
	return m
}

func validSchedule(s string) bool {
	if strings.HasPrefix(s, "@") { // @daily, @hourly, ...
		return len(strings.Fields(s)) == 1
	}
	return len(strings.Fields(s)) == 5
}

func writeCronFile(app string, jobs []cronJob) error {
	path := filepath.Join(cronDir, "gantry-"+app)
	if len(jobs) == 0 {
		os.Remove(path)
		return nil
	}
	if err := os.MkdirAll(cronLogDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(cronDir, 0o755); err != nil { // no-op on real /etc/cron.d
		return err
	}
	var b strings.Builder
	b.WriteString("# managed by gantry, edit via the panel, not by hand\nSHELL=/bin/sh\nPATH=/usr/local/bin:/usr/bin:/bin\n")
	for _, j := range jobs {
		logf := filepath.Join(cronLogDir(), app+"-"+j.ID+".log")
		inner := fmt.Sprintf("dokku --rm run %s %s; echo \"$(date -Is) exit=$?\" >> %s", app, j.Command, logf)
		inner = strings.ReplaceAll(inner, "'", `'\''`)
		inner = strings.ReplaceAll(inner, "%", `\%`) // % is newline in crontab lines
		fmt.Fprintf(&b, "%s root sh -c '%s'\n", j.Schedule, inner)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func lastRun(app, id string) string {
	b, err := os.ReadFile(filepath.Join(cronLogDir(), app+"-"+id+".log"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}
