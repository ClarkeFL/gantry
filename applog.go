package main

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Runtime log history. Docker discards an app's logs on every deploy, so a
// background collector polls `dokku logs` and appends new lines to per-app
// daily files under dataDir/applog/<app>/YYYY-MM-DD.log. Old files are
// pruned after logRetentionDays.

const logRetentionDays = 7

func appLogDir(app string) string { return filepath.Join(dataDir, "applog", app) }

// ponytail: naive keyword/status classification, tune patterns when they misfire
var (
	logErrRe  = regexp.MustCompile(`(?i)\b(error|exception|fatal|panic|traceback)\b|\s5\d\d\s`)
	logWarnRe = regexp.MustCompile(`(?i)\bwarn(ing)?\b|\s4\d\d\s`)
)

func classifyLogLine(line string) string {
	if logErrRe.MatchString(line) {
		return "e"
	}
	if logWarnRe.MatchString(line) {
		return "w"
	}
	return ""
}

// lineTime parses the leading docker/dokku timestamp of a log line.
func lineTime(line string) (time.Time, bool) {
	tok, _, ok := strings.Cut(line, " ")
	if !ok {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, tok); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// lastStoredTime finds the newest timestamp already on disk for an app, so a
// restarted panel doesn't re-append lines it has stored before.
func lastStoredTime(app string) time.Time {
	files, _ := filepath.Glob(filepath.Join(appLogDir(app), "*.log"))
	if len(files) == 0 {
		return time.Time{}
	}
	sort.Strings(files)
	var last time.Time
	f, err := os.Open(files[len(files)-1])
	if err != nil {
		return last
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for sc.Scan() {
		if t, ok := lineTime(sc.Text()); ok {
			last = t
		}
	}
	return last
}

// collectAppLogs runs forever, polling every app's recent log tail and
// storing lines newer than what's already on disk.
// ponytail: 300-line tail every 30s, an app bursting past that loses the
// excess; raise the tail or interval if that ever matters.
func collectAppLogs() {
	lastSeen := map[string]time.Time{}
	for {
		out, err := dokku("--quiet", "apps:list")
		if err == nil {
			for _, app := range strings.Split(out, "\n") {
				app = strings.TrimSpace(app)
				if !appRe.MatchString(app) {
					continue
				}
				if _, ok := lastSeen[app]; !ok {
					lastSeen[app] = lastStoredTime(app)
				}
				collectOne(app, lastSeen)
			}
			pruneAppLogs()
		}
		time.Sleep(30 * time.Second)
	}
}

func collectOne(app string, lastSeen map[string]time.Time) {
	out, err := dokku("logs", app, "-n", "300", "-q")
	if err != nil {
		return
	}
	last := lastSeen[app]
	newest := last
	var pending []string
	for _, line := range strings.Split(out, "\n") {
		line = stripANSI(strings.TrimRight(line, "\r"))
		t, ok := lineTime(line)
		if !ok || !t.After(last) {
			continue
		}
		pending = append(pending, line)
		if t.After(newest) {
			newest = t
		}
	}
	if len(pending) == 0 {
		return
	}
	dir := appLogDir(app)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, time.Now().Format("2006-01-02")+".log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	for _, l := range pending {
		fmt.Fprintln(f, l)
	}
	lastSeen[app] = newest
}

func pruneAppLogs() {
	cutoff := time.Now().AddDate(0, 0, -logRetentionDays).Format("2006-01-02")
	dirs, _ := filepath.Glob(filepath.Join(dataDir, "applog", "*"))
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
		for _, f := range files {
			if strings.TrimSuffix(filepath.Base(f), ".log") < cutoff {
				os.Remove(f)
			}
		}
	}
}

type histLine struct {
	T    int64  `json:"t"` // unix ms
	Line string `json:"line"`
	Sev  string `json:"sev,omitempty"` // "e", "w" or empty
}

const histMaxLines = 3000

// handleLogHistory returns stored log lines for the past N hours.
func handleLogHistory(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours < 1 || hours > 24*logRetentionDays {
		hours = 24
	}
	from := time.Now().Add(-time.Duration(hours) * time.Hour)
	if mockMode {
		writeJSON(w, map[string]any{"lines": mockHistory(from), "retentionDays": logRetentionDays})
		return
	}
	lines := []histLine{}
	for d := 0; d <= hours/24+1; d++ {
		day := from.AddDate(0, 0, d)
		if day.After(time.Now().AddDate(0, 0, 1)) {
			break
		}
		path := filepath.Join(appLogDir(name), day.Format("2006-01-02")+".log")
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			t, ok := lineTime(line)
			if !ok || t.Before(from) {
				continue
			}
			_, rest, _ := strings.Cut(line, " ")
			lines = append(lines, histLine{t.UnixMilli(), rest, classifyLogLine(rest)})
		}
		f.Close()
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].T < lines[j].T })
	if len(lines) > histMaxLines {
		lines = lines[len(lines)-histMaxLines:]
	}
	writeJSON(w, map[string]any{"lines": lines, "retentionDays": logRetentionDays})
}

// mockHistory fabricates a plausible day of traffic with warn/error clusters.
func mockHistory(from time.Time) []histLine {
	lines := []histLine{}
	end := time.Now()
	for t := from; t.Before(end); t = t.Add(time.Minute) {
		m := t.Unix() / 60
		lines = append(lines, histLine{t.UnixMilli(), fmt.Sprintf("app[web.1]: GET /health 200 %dms", 8+m%14), ""})
		if m%9 == 0 {
			lines = append(lines, histLine{t.Add(10 * time.Second).UnixMilli(), "app[web.1]: GET /api/items 200 42ms", ""})
		}
		if m%37 == 0 {
			lines = append(lines, histLine{t.Add(20 * time.Second).UnixMilli(), "app[web.1]: WARN slow query took 1.9s", "w"})
		}
		if m%97 == 0 {
			for i := int64(0); i < 2+m%3; i++ {
				lines = append(lines, histLine{t.Add(time.Duration(25+i) * time.Second).UnixMilli(), "app[web.1]: ERROR upstream timeout: POST /api/sync 502", "e"})
			}
		}
	}
	if len(lines) > histMaxLines {
		lines = lines[len(lines)-histMaxLines:]
	}
	return lines
}
