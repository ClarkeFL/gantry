package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
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
	Name     string `json:"name"`
	Running  bool   `json:"running"`
	Category string `json:"category"`
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
		if name == "" {
			continue
		}
		running, _ := dokku("ps:report", name, "--running")
		apps = append(apps, appInfo{name, running == "true", getMeta(name).Category})
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
	sort.Strings(cats)
	writeJSON(w, map[string]any{"apps": apps, "services": listServices(), "categories": cats})
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
	repo, ref, dockerfile, image := m.Repo, m.Ref, m.Dockerfile, m.Image
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
		"dockerfile": dockerfile,
		"image":      image,
	})
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
		if name == "" {
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
	if mockMode {
		src := "last source"
		if req.Repo != "" {
			src = req.Repo
		} else if req.Image != "" {
			src = req.Image
		}
		for _, l := range []string{"-----> Deploying " + name + " from " + src, "-----> Building...", "-----> Releasing...", "-----> Done"} {
			send(l)
			time.Sleep(400 * time.Millisecond)
		}
		mockMu.Lock()
		mockRunning[name] = true
		mockMu.Unlock()
		send("[gantry] done")
		return
	}
	switch {
	case req.Repo != "":
		if req.Dockerfile != "" {
			dokku("builder-dockerfile:set", name, "dockerfile-path", req.Dockerfile)
		}
		url := req.Repo
		settingsMu.Lock()
		user, tok := settings.GitHubUser, settings.GitHubToken
		settingsMu.Unlock()
		if user != "" && tok != "" && strings.HasPrefix(url, "https://github.com/") {
			url = "https://" + user + ":" + tok + "@" + strings.TrimPrefix(url, "https://")
		}
		args := []string{"git:sync", "--build", name, url}
		if req.Ref != "" {
			args = append(args, req.Ref)
		}
		streamCmd(r.Context(), send, "dokku", args...)
	case req.Image != "":
		streamCmd(r.Context(), send, "dokku", "git:from-image", name, req.Image)
	default:
		streamCmd(r.Context(), send, "dokku", "ps:rebuild", name)
	}
	send("[gantry] done")
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
