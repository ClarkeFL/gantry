package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	appRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	keyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func appName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if !appRe.MatchString(name) {
		httpErr(w, http.StatusBadRequest, "bad app name")
		return "", false
	}
	return name, true
}

type appInfo struct {
	Name         string `json:"name"`
	Running      bool   `json:"running"`
	Category     string `json:"category"`
	LastDeploy   string `json:"lastDeploy,omitempty"`
	LastDeployOK bool   `json:"lastDeployOk"`
}

func handleApps(w http.ResponseWriter, r *http.Request) {
	out, err := dokku("--quiet", "apps:list")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	apps := []appInfo{}
	metaMu.Lock()
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if !appRe.MatchString(name) {
			continue // headers and "! You haven't deployed any applications yet"
		}
		running, _ := dokku("ps:report", name, "--running")
		m := getMeta(name)
		apps = append(apps, appInfo{name, running == "true", m.Category, m.LastDeploy, m.LastDeployOK})
	}
	metaMu.Unlock()
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	catSet := map[string]bool{}
	cats := []string{}
	settingsMu.Lock()
	for _, c := range settings.Categories {
		if !catSet[c] {
			catSet[c] = true
			cats = append(cats, c)
		}
	}
	settingsMu.Unlock()
	for _, a := range apps {
		if a.Category != "" && !catSet[a.Category] {
			catSet[a.Category] = true
			cats = append(cats, a.Category)
		}
	}
	// order = stored list order (user-arranged), app-derived extras appended
	writeJSON(w, map[string]any{"apps": apps, "categories": cats})
}

func handleAppDetail(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	running, _ := dokku("ps:report", name, "--running")
	envJSON, err := dokku("config:export", "--format", "json", name)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	envVars := map[string]string{}
	json.Unmarshal([]byte(envJSON), &envVars)
	domainsOut, _ := dokku("domains:report", name, "--domains-app-vhosts")
	_, sslErr := dokku("letsencrypt:active", name)
	nativeCron, _ := dokku("cron:list", name)
	type domainInfo struct {
		Name  string `json:"name"`
		DNSOK bool   `json:"dnsOk"`
	}
	domains := []domainInfo{}
	myIP := ""
	if !mockMode {
		myIP = serverIP()
	}
	for _, dom := range strings.Fields(domainsOut) {
		ok := false
		if mockMode {
			ok = dom != "www.example.com" // one waiting row for UI dev
		} else if ips, err := lookupTimeout(dom); err == nil {
			for _, ip := range ips {
				if ip == myIP {
					ok = true
					break
				}
			}
		}
		domains = append(domains, domainInfo{dom, ok})
	}
	metaMu.Lock()
	m := getMeta(name)
	jobs := make([]cronJob, len(m.Jobs))
	copy(jobs, m.Jobs)
	category := m.Category
	repo, ref, buildDir, dockerfile, image := m.Repo, m.Ref, m.BuildDir, m.Dockerfile, m.Image
	lastDeploy, lastDeployOK := m.LastDeploy, m.LastDeployOK
	metaMu.Unlock()
	for i := range jobs {
		jobs[i].Last = lastRun(name, jobs[i].ID)
	}
	writeJSON(w, map[string]any{
		"name":       name,
		"running":    running == "true",
		"category":   category,
		"env":        envVars,
		"domains":    domains,
		"ssl":        sslErr == nil,
		"leEmailSet": func() bool { settingsMu.Lock(); defer settingsMu.Unlock(); return settings.LEEmail != "" }(),
		"jobs":       jobs,
		"nativeCron": nativeCron,
		"repo":       repo,
		"ref":        ref,
		"buildDir":     buildDir,
		"dockerfile":   dockerfile,
		"image":        image,
		"lastDeploy":   lastDeploy,
		"lastDeployOk": lastDeployOK,
	})
}

// handleSourceSet persists an app's deploy source and applies builder settings.
func handleSourceSet(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct{ Repo, Ref, BuildDir, Dockerfile, Image string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	for _, p := range []*string{&req.Repo, &req.Ref, &req.BuildDir, &req.Dockerfile, &req.Image} {
		*p = strings.TrimSpace(*p)
	}
	if req.Repo != "" && req.Image != "" {
		httpErr(w, 400, "choose a repo or an image, not both")
		return
	}
	metaMu.Lock()
	m := getMeta(name)
	m.Repo, m.Ref, m.BuildDir, m.Dockerfile, m.Image = req.Repo, req.Ref, req.BuildDir, req.Dockerfile, req.Image
	err := saveMeta()
	metaMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !mockMode && req.Repo != "" {
		if req.BuildDir != "" {
			dokku("builder:set", name, "build-dir", req.BuildDir)
		} else {
			dokku("builder:set", name, "build-dir")
		}
		if req.Dockerfile != "" {
			dokku("builder-dockerfile:set", name, "dockerfile-path", req.Dockerfile)
		} else {
			dokku("builder-dockerfile:set", name, "dockerfile-path")
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleEnv(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct {
		Set     map[string]string `json:"set"`
		Unset   []string          `json:"unset"`
		Restart bool              `json:"restart"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	for k := range req.Set {
		if !keyRe.MatchString(k) {
			httpErr(w, 400, "bad env key: "+k)
			return
		}
	}
	for _, k := range req.Unset {
		if !keyRe.MatchString(k) {
			httpErr(w, 400, "bad env key: "+k)
			return
		}
	}
	if len(req.Set) > 0 {
		args := []string{"config:set", "--no-restart", name}
		keys := make([]string, 0, len(req.Set))
		for k := range req.Set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, k+"="+req.Set[k])
		}
		if _, err := dokku(args...); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	if len(req.Unset) > 0 {
		args := append([]string{"config:unset", "--no-restart", name}, req.Unset...)
		if _, err := dokku(args...); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	if req.Restart {
		if _, err := dokku("ps:restart", name); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleCategory(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct{ Category string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	metaMu.Lock()
	getMeta(name).Category = strings.TrimSpace(req.Category)
	err := saveMeta()
	metaMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handlePs(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct{ Action string }
	json.NewDecoder(r.Body).Decode(&req)
	switch req.Action {
	case "restart", "stop", "start":
	default:
		httpErr(w, 400, "action must be restart, stop or start")
		return
	}
	if out, err := dokku("ps:"+req.Action, name); err != nil {
		httpErr(w, 500, out)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleCronPut(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct{ Jobs []cronJob }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	for i := range req.Jobs {
		j := &req.Jobs[i]
		j.Schedule = strings.TrimSpace(j.Schedule)
		j.Command = strings.TrimSpace(j.Command)
		if !validSchedule(j.Schedule) {
			httpErr(w, 400, "bad schedule: "+j.Schedule)
			return
		}
		if j.Command == "" {
			httpErr(w, 400, "empty command")
			return
		}
		if j.ID == "" {
			j.ID = fmt.Sprintf("%x", time.Now().UnixNano())[8:]
		}
	}
	if err := writeCronFile(name, req.Jobs); err != nil {
		httpErr(w, 500, "writing cron file: "+err.Error())
		return
	}
	metaMu.Lock()
	getMeta(name).Jobs = req.Jobs
	err := saveMeta()
	metaMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"jobs": req.Jobs})
}

func handleServicesGet(w http.ResponseWriter, r *http.Request) {
	type svcOut struct {
		Type     string   `json:"type"`
		Name     string   `json:"name"`
		Status   string   `json:"status"`
		Category string   `json:"category"`
		Links    []string `json:"links"`
	}
	svcs := listServices()
	settingsMu.Lock()
	catSet := map[string]bool{}
	cats := []string{}
	for _, c := range settings.DBCategories {
		if !catSet[c] {
			catSet[c] = true
			cats = append(cats, c)
		}
	}
	out := make([]svcOut, 0, len(svcs))
	for _, s := range svcs {
		cat := settings.DBCategory[s.Type+"/"+s.Name]
		if cat != "" && !catSet[cat] {
			catSet[cat] = true
			cats = append(cats, cat)
		}
		out = append(out, svcOut{s.Type, s.Name, s.Status, cat, nil})
	}
	settingsMu.Unlock()
	for i := range out {
		out[i].Links = serviceLinks(out[i].Type, out[i].Name)
	}
	writeJSON(w, map[string]any{"services": out, "categories": cats})
}

func serviceLinks(t, n string) []string {
	links := []string{}
	if txt, err := dokku(t+":links", n); err == nil {
		for _, line := range strings.Split(txt, "\n") {
			if line = strings.TrimSpace(line); appRe.MatchString(line) {
				links = append(links, line)
			}
		}
	}
	return links
}

func handleServiceLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type, Name, App string
		Unlink          bool
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !serviceTypes[req.Type] || !appRe.MatchString(req.Name) || !appRe.MatchString(req.App) {
		httpErr(w, 400, "bad service or app name")
		return
	}
	verb := ":link"
	if req.Unlink {
		verb = ":unlink"
	}
	if out, err := dokku(req.Type+verb, req.Name, req.App); err != nil {
		httpErr(w, 500, out)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// --- database backups to S3 via dokku's <plugin>:backup ---

func s3Config() (bucket string, authArgs func(t, n string) []string, ok bool) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if settings.S3Bucket == "" || settings.S3Key == "" || settings.S3Secret == "" {
		return "", nil, false
	}
	key, secret, region, endpoint := settings.S3Key, settings.S3Secret, settings.S3Region, settings.S3Endpoint
	return settings.S3Bucket, func(t, n string) []string {
		args := []string{t + ":backup-auth", n, key, secret}
		if endpoint != "" {
			if region == "" {
				region = "us-east-1"
			}
			args = append(args, region, "v4", endpoint)
		} else if region != "" {
			args = append(args, region)
		}
		return args
	}, true
}

func handleBackups(w http.ResponseWriter, r *http.Request) {
	type row struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Schedule string `json:"schedule"`
	}
	rows := []row{}
	for _, s := range listServices() {
		sched := ""
		if out, err := dokku(s.Type+":backup-schedule-cat", s.Name); err == nil {
			for _, line := range strings.Split(out, "\n") {
				f := strings.Fields(line)
				if len(f) >= 5 && !strings.HasPrefix(f[0], "#") && !strings.HasPrefix(f[0], "=") {
					sched = strings.Join(f[:5], " ")
					break
				}
			}
		}
		rows = append(rows, row{s.Type, s.Name, sched})
	}
	bucket, _, s3ok := s3Config()
	writeJSON(w, map[string]any{"databases": rows, "s3Set": s3ok, "bucket": bucket})
}

func handleServiceBackup(w http.ResponseWriter, r *http.Request) {
	var req struct{ Type, Name string }
	json.NewDecoder(r.Body).Decode(&req)
	if !serviceTypes[req.Type] || !appRe.MatchString(req.Name) {
		httpErr(w, 400, "bad service type or name")
		return
	}
	send, ok := sseStart(w)
	if !ok {
		return
	}
	bucket, authArgs, s3ok := s3Config()
	if !s3ok {
		send("[error] configure S3 storage on the Backups page first")
		return
	}
	if mockMode {
		for _, l := range []string{"[check] backup credentials set", "-----> Backing up " + req.Name + " to s3://" + bucket, "-----> Uploading dump…", "-----> Backup complete"} {
			send(l)
			time.Sleep(300 * time.Millisecond)
		}
		send("[gantry] done")
		return
	}
	send("[check] setting backup credentials…")
	if out, err := dokku(authArgs(req.Type, req.Name)...); err != nil {
		send("[error] backup-auth failed: " + out)
		return
	}
	if err := streamCmd(r.Context(), send, "dokku", req.Type+":backup", req.Name, bucket); err != nil {
		send("[error] backup failed (" + err.Error() + ") — see output above")
		go notifyWebhook("gantry: backup failed for " + req.Type + "/" + req.Name)
		return
	}
	send("[gantry] done")
}

func handleBackupSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct{ Type, Name, Schedule string }
	json.NewDecoder(r.Body).Decode(&req)
	if !serviceTypes[req.Type] || !appRe.MatchString(req.Name) {
		httpErr(w, 400, "bad service type or name")
		return
	}
	req.Schedule = strings.TrimSpace(req.Schedule)
	if req.Schedule == "" {
		if out, err := dokku(req.Type+":backup-unschedule", req.Name); err != nil {
			httpErr(w, 500, out)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if !validSchedule(req.Schedule) || strings.HasPrefix(req.Schedule, "@") {
		httpErr(w, 400, "schedule must be 5 cron fields, e.g. 0 3 * * *")
		return
	}
	bucket, authArgs, s3ok := s3Config()
	if !s3ok {
		httpErr(w, 400, "configure S3 storage first")
		return
	}
	if out, err := dokku(authArgs(req.Type, req.Name)...); err != nil {
		httpErr(w, 500, out)
		return
	}
	if out, err := dokku(req.Type+":backup-schedule", req.Name, req.Schedule, bucket); err != nil {
		httpErr(w, 500, out)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleAppDestroy(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	if out, err := dokku("--force", "apps:destroy", name); err != nil {
		httpErr(w, 500, out)
		return
	}
	writeCronFile(name, nil) // removes /etc/cron.d/gantry-<name>
	os.Remove(deployLogPath(name))
	metaMu.Lock()
	delete(meta, name)
	saveMeta()
	metaMu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func handleServiceDestroy(w http.ResponseWriter, r *http.Request) {
	var req struct{ Type, Name string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !serviceTypes[req.Type] || !appRe.MatchString(req.Name) {
		httpErr(w, 400, "bad service type or name")
		return
	}
	if out, err := dokku("--force", req.Type+":destroy", req.Name); err != nil {
		httpErr(w, 500, out)
		return
	}
	settingsMu.Lock()
	delete(settings.DBCategory, req.Type+"/"+req.Name)
	saveSettings()
	settingsMu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, Category string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !appRe.MatchString(req.Name) {
		httpErr(w, 400, "app names must be lowercase letters, digits, . or -")
		return
	}
	if out, err := dokku("apps:create", req.Name); err != nil {
		httpErr(w, 500, out)
		return
	}
	if c := strings.TrimSpace(req.Category); c != "" {
		metaMu.Lock()
		getMeta(req.Name).Category = c
		saveMeta()
		metaMu.Unlock()
	}
	writeJSON(w, map[string]any{"ok": true})
}

var serviceTypes = map[string]bool{"postgres": true, "mysql": true, "mariadb": true, "redis": true, "mongo": true}

func handleCreateService(w http.ResponseWriter, r *http.Request) {
	var req struct{ Type, Name string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !serviceTypes[req.Type] || !appRe.MatchString(req.Name) {
		httpErr(w, 400, "bad service type or name")
		return
	}
	send, ok := sseStart(w)
	if !ok {
		return
	}
	if mockMode {
		send("-----> Creating " + req.Name + "...")
		mockMu.Lock()
		mockServices = append(mockServices, service{req.Type, req.Name, "running"})
		saveMockState()
		mockMu.Unlock()
		send("[gantry] done")
		return
	}
	// first create on a plugin pulls its image — stream so the UI shows progress
	streamCmd(r.Context(), send, "dokku", req.Type+":create", req.Name)
	send("[gantry] done")
}

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func handleDomainsMod(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct{ Action, Domain string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if !domainRe.MatchString(req.Domain) {
		httpErr(w, 400, "that doesn't look like a domain")
		return
	}
	var cmd string
	switch req.Action {
	case "add":
		cmd = "domains:add"
	case "remove":
		cmd = "domains:remove"
	default:
		httpErr(w, 400, "action must be add or remove")
		return
	}
	if out, err := dokku(cmd, name, req.Domain); err != nil {
		httpErr(w, 500, out)
		return
	}
	// If the new domain already resolves to this server, the UI can kick off
	// Let's Encrypt immediately instead of making the user click.
	dnsOk := false
	if req.Action == "add" {
		if mockMode {
			dnsOk = true
		} else if ips, err := net.LookupHost(req.Domain); err == nil {
			my := serverIP()
			for _, ip := range ips {
				if ip == my {
					dnsOk = true
					break
				}
			}
		}
	}
	writeJSON(w, map[string]any{"ok": true, "dnsOk": dnsOk})
}

func lookupTimeout(host string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupHost(ctx, host)
}

func serverIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// handleSSL streams `dokku letsencrypt:enable <app>` and registers the renewal cron.
func handleSSL(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	send, ok := sseStart(w)
	if !ok {
		return
	}
	settingsMu.Lock()
	email := settings.LEEmail
	settingsMu.Unlock()
	if email == "" {
		send("[error] set your Let's Encrypt email in Settings first")
		return
	}
	if mockMode {
		send("-----> Enabling letsencrypt for " + name)
		send("-----> Certificate retrieved and installed")
		mockMu.Lock()
		mockSSL[name] = true
		saveMockState()
		mockMu.Unlock()
		send("[gantry] done")
		return
	}
	streamCmd(r.Context(), send, "dokku", "letsencrypt:enable", name)
	dokku("letsencrypt:cron-job", "--add") // idempotent auto-renew
	send("[gantry] done")
}

func handleDomains(w http.ResponseWriter, r *http.Request) {
	out, err := dokku("--quiet", "apps:list")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	type row struct {
		Domain string `json:"domain"`
		App    string `json:"app"`
	}
	rows := []row{}
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if !appRe.MatchString(name) {
			continue
		}
		d, _ := dokku("domains:report", name, "--domains-app-vhosts")
		for _, dom := range strings.Fields(d) {
			rows = append(rows, row{dom, name})
		}
	}
	writeJSON(w, map[string]any{"domains": rows})
}

// --- streaming (SSE over plain fetch) ---

func sseStart(w http.ResponseWriter) (func(string), bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming unsupported")
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	return func(line string) {
		fmt.Fprintf(w, "data: %s\n\n", line)
		fl.Flush()
	}, true
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	send, ok := sseStart(w)
	if !ok {
		return
	}
	if mockMode {
		for i := 0; i < 25; i++ {
			send(fmt.Sprintf("%s app[web.1]: GET /health 200 %dms", time.Now().Add(time.Duration(i-25)*time.Minute).Format(time.RFC3339), 10+i))
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case t := <-ticker.C:
				send(t.Format(time.RFC3339) + " app[web.1]: GET / 200 12ms")
			}
		}
	}
	streamCmd(r.Context(), send, "dokku", "logs", name, "-t", "-n", "100")
}

func deployLogPath(name string) string {
	return filepath.Join(dataDir, "deploylog", name+".log")
}

func handleDeployLog(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	b, err := os.ReadFile(deployLogPath(name))
	if err != nil {
		b = []byte("No deploys yet.")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(b)
}

func handleDeploy(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct{ Image, Repo, Ref, Dockerfile string }
	json.NewDecoder(r.Body).Decode(&req)
	req.Repo, req.Image = strings.TrimSpace(req.Repo), strings.TrimSpace(req.Image)
	send, ok := sseStart(w)
	if !ok {
		return
	}
	// tee every line into the per-app deploy log (fresh file per deploy)
	os.MkdirAll(filepath.Join(dataDir, "deploylog"), 0o755)
	if f, err := os.Create(deployLogPath(name)); err == nil {
		defer f.Close()
		fmt.Fprintf(f, "=== deploy started %s\n", time.Now().Format(time.RFC3339))
		sse := send
		send = func(line string) {
			fmt.Fprintln(f, line)
			sse(line)
		}
	}
	if req.Repo != "" || req.Image != "" {
		// remember the source so the next plain Deploy redeploys the same thing
		metaMu.Lock()
		m := getMeta(name)
		m.Repo, m.Ref, m.Dockerfile, m.Image = req.Repo, strings.TrimSpace(req.Ref), strings.TrimSpace(req.Dockerfile), req.Image
		saveMeta()
		metaMu.Unlock()
	} else {
		// no source given → redeploy from the stored one
		metaMu.Lock()
		m := getMeta(name)
		req.Repo, req.Ref, req.Dockerfile, req.Image = m.Repo, m.Ref, m.Dockerfile, m.Image
		metaMu.Unlock()
	}
	finish := func(ok bool, detail string) {
		metaMu.Lock()
		m := getMeta(name)
		m.LastDeploy, m.LastDeployOK = time.Now().Format(time.RFC3339), ok
		saveMeta()
		metaMu.Unlock()
		if ok {
			send("[gantry] done")
			return
		}
		if detail != "" {
			send("[error] " + detail)
		}
		send("[gantry] aborted — nothing was deployed")
		go notifyWebhook("gantry: deploy failed for " + name + " — " + detail)
	}
	if mockMode {
		src := "last source"
		if req.Repo != "" {
			src = req.Repo
		} else if req.Image != "" {
			src = req.Image
		}
		if req.Repo != "" || req.Image != "" {
			send("[check] verifying source…")
			time.Sleep(300 * time.Millisecond)
			send("[check] source ok")
		}
		for _, l := range []string{"-----> Deploying " + name + " from " + src, "-----> Building...", "-----> Releasing...", "-----> Done"} {
			send(l)
			time.Sleep(400 * time.Millisecond)
		}
		mockMu.Lock()
		mockRunning[name] = true
		saveMockState()
		mockMu.Unlock()
		finish(true, "")
		return
	}
	var runErr error
	switch {
	case req.Repo != "":
		url := req.Repo
		settingsMu.Lock()
		user, tok := settings.GitHubUser, settings.GitHubToken
		settingsMu.Unlock()
		if user != "" && tok != "" && strings.HasPrefix(url, "https://github.com/") {
			url = "https://" + user + ":" + tok + "@" + strings.TrimPrefix(url, "https://")
		}
		// pre-flight: repo reachable, auth ok, branch exists — before any build starts
		send("[check] verifying repository access…")
		checkArgs := []string{"ls-remote", "--exit-code", url}
		if req.Ref != "" {
			checkArgs = append(checkArgs, req.Ref)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		out, err := exec.CommandContext(ctx, "git", checkArgs...).CombinedOutput()
		cancel()
		if err != nil {
			detail := strings.TrimSpace(strings.ReplaceAll(string(out), tok, "•••"))
			if req.Ref != "" && strings.Contains(err.Error(), "exit status 2") {
				detail = "branch or tag '" + req.Ref + "' not found in the repository"
			}
			finish(false, "repository check failed: "+detail)
			return
		}
		send("[check] repository ok" + map[bool]string{true: ", branch '" + req.Ref + "' found", false: ""}[req.Ref != ""])
		args := []string{"git:sync", "--build", name, url}
		if req.Ref != "" {
			args = append(args, req.Ref)
		}
		runErr = streamCmd(r.Context(), send, "dokku", args...)
	case req.Image != "":
		send("[check] verifying image exists…")
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		out, err := exec.CommandContext(ctx, "docker", "manifest", "inspect", req.Image).CombinedOutput()
		cancel()
		if err != nil {
			finish(false, "image check failed: "+strings.TrimSpace(string(out)))
			return
		}
		send("[check] image found")
		runErr = streamCmd(r.Context(), send, "dokku", "git:from-image", name, req.Image)
	default:
		runErr = streamCmd(r.Context(), send, "dokku", "ps:rebuild", name)
	}
	if runErr != nil {
		finish(false, "deploy exited with an error ("+runErr.Error()+") — see the output above")
		return
	}
	finish(true, "")
}

// --- self-update: download latest release binary, swap self, exit; systemd restarts us ---

var updateCache struct {
	sync.Mutex
	latest string
	at     time.Time
}

func latestVersion() string {
	repo := env("GANTRY_REPO", "")
	if repo == "" {
		return ""
	}
	updateCache.Lock()
	defer updateCache.Unlock()
	if time.Since(updateCache.at) < time.Hour {
		return updateCache.latest
	}
	client := &http.Client{Timeout: 10 * time.Second}
	greq, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if tok := githubToken(); tok != "" {
		greq.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(greq)
	if err != nil {
		return updateCache.latest
	}
	defer resp.Body.Close()
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if json.NewDecoder(resp.Body).Decode(&rel) == nil && rel.TagName != "" {
		updateCache.latest, updateCache.at = rel.TagName, time.Now()
	}
	return updateCache.latest
}

func handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if mockMode {
		writeJSON(w, map[string]any{"current": version, "latest": "v9.9.9", "available": true})
		return
	}
	latest := latestVersion()
	writeJSON(w, map[string]any{
		"current":   version,
		"latest":    latest,
		"available": latest != "" && latest != version,
	})
}

func handleUpdate(w http.ResponseWriter, r *http.Request) {
	if mockMode { // let UI dev exercise the flow without swapping the dev binary
		writeJSON(w, map[string]any{"ok": true, "restarting": true})
		return
	}
	repo := env("GANTRY_REPO", "")
	if repo == "" {
		httpErr(w, 400, "GANTRY_REPO not set (e.g. youruser/gantry)")
		return
	}
	url := fmt.Sprintf("https://github.com/%s/releases/latest/download/gantry-linux-%s", repo, runtime.GOARCH)
	resp, err := http.Get(url)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		httpErr(w, 500, fmt.Sprintf("download failed: %s (%s)", resp.Status, url))
		return
	}
	exe, err := os.Executable()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	tmp := exe + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if _, err := f.ReadFrom(resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		httpErr(w, 500, err.Error())
		return
	}
	f.Close()
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "restarting": true})
	go func() { // let the response flush, then let systemd bring up the new binary
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
}
