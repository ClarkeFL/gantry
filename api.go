package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
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
	Group        string `json:"group"`
	LastDeploy   string `json:"lastDeploy,omitempty"`
	LastDeployOK bool   `json:"lastDeployOk"`
	Maintenance  bool   `json:"maintenance"`
}

func handleApps(w http.ResponseWriter, r *http.Request) {
	out, err := dokku("--quiet", "apps:list")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	maint := maintenanceAll()
	names := []string{}
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if appRe.MatchString(name) { // skips headers and "! You haven't deployed any applications yet"
			names = append(names, name)
		}
	}
	// one ps:report shell-out per app; run them concurrently or the list
	// grows ~250ms slower per app
	apps := make([]appInfo, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			running, _ := dokku("ps:report", name, "--running")
			metaMu.Lock()
			m := getMeta(name)
			apps[i] = appInfo{name, running == "true", m.Category, m.Group, m.LastDeploy, m.LastDeployOK, maint[name]}
			metaMu.Unlock()
		}(i, name)
	}
	wg.Wait()
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	catSet := map[string]bool{}
	cats := []string{}
	settingsMu.Lock()
	for _, c := range settings.Projects {
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
	// letsencrypt:active prints true/false and exits 0 either way
	sslOut, sslErr := dokku("letsencrypt:active", name)
	sslOn := sslErr == nil && strings.TrimSpace(sslOut) == "true"
	nativeCron, _ := dokku("cron:list", name)
	type domainInfo struct {
		Name    string `json:"name"`
		DNSOK   bool   `json:"dnsOk"`
		Proxied bool   `json:"proxied,omitempty"`
	}
	domains := []domainInfo{}
	myIP := ""
	if !mockMode {
		myIP = serverIP()
	}
	for _, dom := range strings.Fields(domainsOut) {
		ok, proxied := false, false
		if mockMode {
			ok = dom != "www.example.com" // one waiting row for UI dev
			proxied = strings.HasPrefix(dom, "blog.")
		} else if ips, err := lookupTimeout(dom); err == nil {
			for _, ip := range ips {
				if ip == myIP {
					ok = true
					break
				}
			}
			if !ok && behindProxy(ips) {
				ok, proxied = true, true // traffic reaches us through the proxy
			}
		}
		domains = append(domains, domainInfo{dom, ok, proxied})
	}
	metaMu.Lock()
	m := getMeta(name)
	jobs := make([]cronJob, len(m.Jobs))
	copy(jobs, m.Jobs)
	category := m.Category
	repo, ref, buildDir, dockerfile, image := m.Repo, m.Ref, m.BuildDir, m.Dockerfile, m.Image
	lastDeploy, lastDeployOK := m.LastDeploy, m.LastDeployOK
	maintTpl := m.MaintenanceTpl
	metaMu.Unlock()
	for i := range jobs {
		jobs[i].Last = lastRun(name, jobs[i].ID)
	}
	settingsMu.Lock()
	projectEnv := map[string]string{}
	for k, v := range settings.ProjectEnv[category] {
		projectEnv[k] = v
	}
	projects := append([]string{}, settings.Projects...)
	settingsMu.Unlock()
	writeJSON(w, map[string]any{
		"name":           name,
		"running":        running == "true",
		"category":       category,
		"env":            envVars,
		"projectEnv":     projectEnv,
		"projects":       projects,
		"domains":        domains,
		"ssl":            sslOn,
		"leEmailSet":     func() bool { settingsMu.Lock(); defer settingsMu.Unlock(); return settings.LEEmail != "" }(),
		"jobs":           jobs,
		"nativeCron":     nativeCron,
		"repo":           repo,
		"ref":            ref,
		"buildDir":       buildDir,
		"dockerfile":     dockerfile,
		"image":          image,
		"lastDeploy":     lastDeploy,
		"lastDeployOk":   lastDeployOK,
		"maintenance":    maintenanceAll()[name],
		"maintenanceTpl": maintTpl,
		"mounts":         listMounts(name),
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
	if err := applyEnv(name, req.Set, req.Unset); err != nil {
		httpErr(w, 500, err.Error())
		return
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
	project := strings.TrimSpace(req.Category)
	metaMu.Lock()
	prev := getMeta(name).Category
	getMeta(name).Category = project
	err := saveMeta()
	metaMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// joining a project inherits its shared env (existing app values win)
	if project != "" && !strings.EqualFold(project, prev) {
		applyProjectEnvToApp(project, name)
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleAppGroup sets an app's sub-group within its project.
func handleAppGroup(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct{ Group string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	metaMu.Lock()
	getMeta(name).Group = strings.TrimSpace(req.Group)
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
	for _, c := range settings.Projects {
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
	writeJSON(w, map[string]any{
		"services":   out,
		"categories": cats,
		"plugins":    installedServicePlugins(),
	})
}

// Official dokku service plugins (name → git URL). Install is optional and
// user-triggered from the Databases page so unused engines cost nothing.
var servicePluginRepos = map[string]string{
	"postgres": "https://github.com/dokku/dokku-postgres.git",
	"mysql":    "https://github.com/dokku/dokku-mysql.git",
	"mariadb":  "https://github.com/dokku/dokku-mariadb.git",
	"redis":    "https://github.com/dokku/dokku-redis.git",
	"mongo":    "https://github.com/dokku/dokku-mongo.git",
}

// servicePluginOrder is the stable UI order for type buttons.
var servicePluginOrder = []string{"postgres", "mysql", "mariadb", "redis", "mongo"}

func installedServicePlugins() map[string]bool {
	out := make(map[string]bool, len(servicePluginOrder))
	for _, t := range servicePluginOrder {
		out[t] = servicePluginInstalled(t)
	}
	return out
}

func servicePluginInstalled(name string) bool {
	if mockMode {
		mockMu.Lock()
		defer mockMu.Unlock()
		if mockPlugins == nil {
			return false
		}
		return mockPlugins[name]
	}
	_, err := dokku("plugin:installed", name)
	return err == nil
}

// handleInstallPlugin streams `dokku plugin:install <url> <name>` so the UI
// can show progress (first install downloads the plugin repo).
func handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Type string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	url, ok := servicePluginRepos[req.Type]
	if !ok {
		httpErr(w, 400, "unknown database type")
		return
	}
	if servicePluginInstalled(req.Type) {
		httpErr(w, 400, req.Type+" plugin is already installed")
		return
	}
	send, ok := sseStart(w)
	if !ok {
		return
	}
	if mockMode {
		send("-----> Installing " + req.Type + " plugin (mock)...")
		mockMu.Lock()
		if mockPlugins == nil {
			mockPlugins = map[string]bool{}
		}
		mockPlugins[req.Type] = true
		saveMockState()
		mockMu.Unlock()
		send("-----> Plugin " + req.Type + " installed")
		send("[gantry] done")
		return
	}
	send("-----> Installing dokku " + req.Type + " plugin...")
	if err := streamCmd(context.Background(), send, "dokku", "plugin:install", url, req.Type); err != nil {
		send("[gantry] error: " + err.Error())
		return
	}
	send("[gantry] done")
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

// --- per-app restore: reapply one app's definition from a file or an S3 archive ---

type appRestoreDef struct {
	Name       string            `json:"name"`
	Env        map[string]string `json:"env"`
	Domains    []string          `json:"domains"`
	Repo       string            `json:"repo"`
	Ref        string            `json:"ref"`
	BuildDir   string            `json:"buildDir"`
	Dockerfile string            `json:"dockerfile"`
	Image      string            `json:"image"`
	Cron       []cronJob         `json:"cron"`
	Category   string            `json:"category"`
}

func applyAppRestore(name string, def appRestoreDef) error {
	dokku("apps:create", name) // no-op if it already exists
	if len(def.Env) > 0 {
		args := []string{"config:set", "--no-restart", name}
		for k, v := range def.Env {
			if keyRe.MatchString(k) {
				args = append(args, k+"="+v)
			}
		}
		if len(args) > 3 {
			if out, err := dokku(args...); err != nil {
				return fmt.Errorf("env restore failed: %s", out)
			}
		}
	}
	existing, _ := dokku("domains:report", name, "--domains-app-vhosts")
	have := map[string]bool{}
	for _, d := range strings.Fields(existing) {
		have[d] = true
	}
	for _, d := range def.Domains {
		if domainRe.MatchString(d) && !have[d] {
			dokku("domains:add", name, d)
		}
	}
	if def.BuildDir != "" {
		dokku("builder:set", name, "build-dir", def.BuildDir)
	}
	if def.Dockerfile != "" {
		dokku("builder-dockerfile:set", name, "dockerfile-path", def.Dockerfile)
	}
	metaMu.Lock()
	m := getMeta(name)
	m.Repo, m.Ref, m.BuildDir, m.Dockerfile, m.Image = def.Repo, def.Ref, def.BuildDir, def.Dockerfile, def.Image
	m.Category = def.Category
	jobs := []cronJob{}
	for _, j := range def.Cron {
		if validSchedule(j.Schedule) && j.Command != "" {
			if j.ID == "" {
				b := make([]byte, 4)
				rand.Read(b)
				j.ID = hex.EncodeToString(b)
			}
			jobs = append(jobs, cronJob{ID: j.ID, Schedule: j.Schedule, Command: j.Command})
		}
	}
	m.Jobs = jobs
	saveMeta()
	metaMu.Unlock()
	return writeCronFile(name, jobs)
}

// defFromArchive digs one app's definition out of a server backup archive.
func defFromArchive(archive []byte, name string) (appRestoreDef, error) {
	def := appRestoreDef{Name: name}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return def, err
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		switch filepath.ToSlash(hdr.Name) {
		case "apps.json":
			var apps []appBackup
			b, _ := io.ReadAll(tr)
			json.Unmarshal(b, &apps)
			for _, a := range apps {
				if a.Name == name {
					def.Env, def.Domains = a.Env, a.Domains
					found = true
				}
			}
		case "state/meta.json":
			var metas map[string]*appMeta
			b, _ := io.ReadAll(tr)
			json.Unmarshal(b, &metas)
			if m := metas[name]; m != nil {
				def.Repo, def.Ref, def.BuildDir, def.Dockerfile, def.Image = m.Repo, m.Ref, m.BuildDir, m.Dockerfile, m.Image
				def.Cron, def.Category = m.Jobs, m.Category
			}
		}
	}
	if !found {
		return def, fmt.Errorf("app %q not found in that backup", name)
	}
	return def, nil
}

func handleBackupArchiveList(w http.ResponseWriter, r *http.Request) {
	if mockMode {
		writeJSON(w, map[string]any{"keys": []string{"gantry/panel-20260719-040000.tar.gz", "gantry/panel-20260718-040000.tar.gz"}})
		return
	}
	keys, err := s3List("gantry/panel-")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// newest first
	for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
		keys[i], keys[j] = keys[j], keys[i]
	}
	writeJSON(w, map[string]any{"keys": keys})
}

func handleBackupArchiveApps(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if mockMode {
		writeJSON(w, map[string]any{"apps": []string{"api", "blog", "landing"}})
		return
	}
	archive, err := s3Get(key)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	names := []string{}
	if gz, err := gzip.NewReader(bytes.NewReader(archive)); err == nil {
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err != nil {
				break
			}
			if filepath.ToSlash(hdr.Name) == "apps.json" {
				var apps []appBackup
				b, _ := io.ReadAll(tr)
				json.Unmarshal(b, &apps)
				for _, a := range apps {
					names = append(names, a.Name)
				}
			}
		}
	}
	writeJSON(w, map[string]any{"apps": names})
}

// --- busy tracking: the panel refuses to self-update while a long-running
// operation (deploy, backup, restore, certificate request) is in flight,
// because restarting would kill it mid-way. ---

var (
	busyOps     sync.Map // id -> label
	busyCounter int64
	busyMu      sync.Mutex
)

func opStart(label string) func() {
	busyMu.Lock()
	busyCounter++
	id := busyCounter
	busyMu.Unlock()
	busyOps.Store(id, label)
	return func() { busyOps.Delete(id) }
}

func busyWith() string {
	label := ""
	busyOps.Range(func(_, v any) bool { label = v.(string); return false })
	return label
}

func handleAppRestore(w http.ResponseWriter, r *http.Request) {
	defer opStart("app restore")()
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct {
		Key string         `json:"key"`
		Def *appRestoreDef `json:"def"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	var def appRestoreDef
	switch {
	case req.Def != nil:
		def = *req.Def
	case req.Key != "":
		if mockMode {
			// pretend the archive holds the app's current definition
			def = appRestoreDef{Name: name}
		} else {
			archive, err := s3Get(req.Key)
			if err != nil {
				httpErr(w, 500, err.Error())
				return
			}
			def, err = defFromArchive(archive, name)
			if err != nil {
				httpErr(w, 404, err.Error())
				return
			}
		}
	default:
		httpErr(w, 400, "provide a backup key or a definition")
		return
	}
	if err := applyAppRestore(name, def); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "env": len(def.Env), "domains": len(def.Domains)})
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
		Enabled  bool   `json:"enabled"`
	}
	settingsMu.Lock()
	dbCron := map[string]string{}
	for k, v := range settings.DBBackupCron {
		dbCron[k] = v
	}
	srvCron, srvPaused := settings.ServerBackupCron, settings.ServerBackupPaused
	settingsMu.Unlock()
	rows := []row{}
	for _, s := range listServices() {
		// live dokku schedule is the source of truth for "enabled"
		live := ""
		if out, err := dokku(s.Type+":backup-schedule-cat", s.Name); err == nil {
			for _, line := range strings.Split(out, "\n") {
				f := strings.Fields(line)
				if len(f) >= 5 && !strings.HasPrefix(f[0], "#") && !strings.HasPrefix(f[0], "=") {
					live = strings.Join(f[:5], " ")
					break
				}
			}
		}
		sched := live
		if sched == "" {
			sched = dbCron[s.Type+"/"+s.Name] // remembered while toggled off
		}
		rows = append(rows, row{s.Type, s.Name, sched, live != ""})
	}
	bucket, _, s3ok := s3Config()
	serverSched := srvCron
	live := ""
	if b, err := os.ReadFile(filepath.Join(cronDir, "gantry-backup")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 5 && !strings.HasPrefix(f[0], "#") && !strings.HasPrefix(f[0], "SHELL") && !strings.HasPrefix(f[0], "PATH") {
				live = strings.Join(f[:5], " ")
				break
			}
		}
	}
	if live != "" {
		serverSched = live // pre-toggle installs: cron file exists, settings empty
	}
	writeJSON(w, map[string]any{
		"databases":      rows,
		"s3Set":          s3ok,
		"bucket":         bucket,
		"serverSchedule": serverSched,
		"serverEnabled":  live != "" && !srvPaused,
		"serverKeep":     backupKeep(),
		"lastBackup":     lastServerBackup(),
	})
}

func handleServerBackup(w http.ResponseWriter, r *http.Request) {
	defer opStart("server backup")()
	send, ok := sseStart(w)
	if !ok {
		return
	}
	if _, _, s3ok := s3Config(); !s3ok {
		send("[error] configure S3 storage first")
		return
	}
	if mockMode {
		for _, l := range []string{"[backup] collecting panel state and app definitions…", "[backup] uploading 42 KB to s3://mock-bucket/gantry/panel-mock.tar.gz", "[backup] done, gantry/panel-mock.tar.gz"} {
			send(l)
			time.Sleep(300 * time.Millisecond)
		}
		logBackup("ok gantry/panel-mock.tar.gz (42 KB, keep " + fmt.Sprint(backupKeep()) + ")")
		send("[gantry] done")
		return
	}
	if err := runServerBackup(send); err != nil {
		send("[error] " + err.Error())
		return
	}
	send("[gantry] done")
}

func handleServerBackupSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Schedule string
		Keep     int
		Enabled  bool
	}
	json.NewDecoder(r.Body).Decode(&req)
	req.Schedule = strings.TrimSpace(req.Schedule)
	if req.Schedule != "" && (!validSchedule(req.Schedule) || strings.HasPrefix(req.Schedule, "@")) {
		httpErr(w, 400, "schedule must be 5 cron fields, e.g. 0 4 * * *")
		return
	}
	if req.Keep < 1 || req.Keep > 100 {
		req.Keep = 7
	}
	settingsMu.Lock()
	settings.BackupKeep = req.Keep
	settings.ServerBackupCron = req.Schedule // remembered even while off
	settings.ServerBackupPaused = !req.Enabled
	saveSettings()
	settingsMu.Unlock()
	path := filepath.Join(cronDir, "gantry-backup")
	if req.Schedule == "" || !req.Enabled {
		os.Remove(path)
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "/usr/local/bin/gantry"
	}
	content := "# managed by gantry, scheduled full server backup\n" +
		req.Schedule + " root " + exe + " backup >/dev/null 2>&1\n"
	os.MkdirAll(cronDir, 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleServiceBackup(w http.ResponseWriter, r *http.Request) {
	defer opStart("database backup")()
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
	if err := streamCmd(context.Background(), send, "dokku", req.Type+":backup", req.Name, bucket); err != nil {
		send("[error] backup failed (" + err.Error() + "), see output above")
		go notifyWebhook("gantry: backup failed for " + req.Type + "/" + req.Name)
		return
	}
	send("[gantry] done")
}

func handleBackupSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type, Name, Schedule string
		Enabled              bool
	}
	json.NewDecoder(r.Body).Decode(&req)
	if !serviceTypes[req.Type] || !appRe.MatchString(req.Name) {
		httpErr(w, 400, "bad service type or name")
		return
	}
	req.Schedule = strings.TrimSpace(req.Schedule)
	key := req.Type + "/" + req.Name
	remember := func() {
		settingsMu.Lock()
		if settings.DBBackupCron == nil {
			settings.DBBackupCron = map[string]string{}
		}
		if req.Schedule != "" {
			settings.DBBackupCron[key] = req.Schedule
		}
		saveSettings()
		settingsMu.Unlock()
	}
	if req.Schedule == "" || !req.Enabled {
		if out, err := dokku(req.Type+":backup-unschedule", req.Name); err != nil && !strings.Contains(strings.ToLower(out), "no schedule") {
			httpErr(w, 500, out)
			return
		}
		remember()
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
	remember()
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
	os.RemoveAll(deployDir(name))
	os.RemoveAll(appLogDir(name))
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
		applyProjectEnvToApp(c, req.Name)
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
	if !servicePluginInstalled(req.Type) {
		httpErr(w, 400, req.Type+" plugin is not installed. Install it from the Databases page first.")
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
	// first create on a plugin pulls its image, stream so the UI shows progress
	streamCmd(context.Background(), send, "dokku", req.Type+":create", req.Name)
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
			if !dnsOk && behindProxy(ips) {
				dnsOk = true
			}
		}
	}
	writeJSON(w, map[string]any{"ok": true, "dnsOk": dnsOk})
}

// Cloudflare's published proxy ranges (cloudflare.com/ips, stable for years).
// A proxied domain resolves to these instead of the server IP, so the plain
// "does it point at us" check would wait forever.
var cloudflareNets = func() []netip.Prefix {
	cidrs := []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	}
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		if p, err := netip.ParsePrefix(c); err == nil {
			out = append(out, p)
		}
	}
	return out
}()

func behindProxy(ips []string) bool {
	for _, s := range ips {
		a, err := netip.ParseAddr(s)
		if err != nil {
			continue
		}
		for _, p := range cloudflareNets {
			if p.Contains(a) {
				return true
			}
		}
	}
	return false
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
	defer opStart("certificate request")()
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
	if out, err := dokku("letsencrypt:set", name, "email", email); err != nil {
		send("[error] could not set the certificate email: " + out)
		return
	}
	if err := streamCmd(context.Background(), send, "dokku", "letsencrypt:enable", name); err != nil {
		send("[error] certificate request failed, see the output above")
		return
	}
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
		fmt.Fprintf(w, "data: %s\n\n", stripANSI(line))
		fl.Flush()
	}, true
}

// stripANSI drops terminal color and cursor codes from dokku/docker output,
// which would otherwise show as garbage like "[36m" in the browser.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\r`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

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
	id := r.URL.Query().Get("id")
	if id != "" && !deployIDRe.MatchString(id) {
		httpErr(w, 400, "bad deploy id")
		return
	}
	if id == "" {
		if ids := deployIDs(name); len(ids) > 0 {
			id = ids[0]
		}
	}
	var b []byte
	var err error
	if id != "" {
		b, err = os.ReadFile(filepath.Join(deployDir(name), id+".log"))
	} else {
		b, err = os.ReadFile(deployLogPath(name)) // pre-history single-file layout
	}
	if err != nil {
		b = []byte("No deploys yet.")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(stripANSI(string(b)))) // logs written before stripping existed
}

func handleDeploy(w http.ResponseWriter, r *http.Request) {
	defer opStart("deploy")()
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
	// tee every line into this deploy's own log file (kept as history)
	os.MkdirAll(deployDir(name), 0o755)
	pruneDeployLogs(name)
	deployID := time.Now().UTC().Format("20060102-150405")
	var logF *os.File
	if f, err := os.Create(filepath.Join(deployDir(name), deployID+".log")); err == nil {
		logF = f
		defer f.Close()
		fmt.Fprintf(f, "=== deploy started %s\n", time.Now().Format(time.RFC3339))
		sse := send
		send = func(line string) {
			fmt.Fprintln(f, stripANSI(line))
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
	// the source is known now that meta has been consulted; record it for history
	if logF != nil {
		src := "rebuild of last deployed code"
		if req.Repo != "" {
			src = req.Repo
			if req.Ref != "" {
				src += " @ " + req.Ref
			}
		} else if req.Image != "" {
			src = req.Image
		}
		fmt.Fprintf(logF, "=== source %s\n", src)
	}
	finish := func(ok bool, detail string) {
		metaMu.Lock()
		m := getMeta(name)
		m.LastDeploy, m.LastDeployOK = time.Now().Format(time.RFC3339), ok
		saveMeta()
		metaMu.Unlock()
		defer func() {
			if logF != nil {
				fmt.Fprintf(logF, "=== deploy finished %s %s\n", map[bool]string{true: "ok", false: "failed"}[ok], time.Now().Format(time.RFC3339))
			}
		}()
		if ok {
			send("[gantry] done")
			return
		}
		if detail != "" {
			send("[error] " + detail)
		}
		send("[gantry] aborted, nothing was deployed")
		go notifyWebhook("gantry: deploy failed for " + name + ", " + detail)
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
		// pre-flight: repo reachable, auth ok, branch exists, before any build starts
		send("[check] verifying repository access…")
		checkArgs := []string{"ls-remote", "--exit-code", url}
		if req.Ref != "" {
			checkArgs = append(checkArgs, req.Ref)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		out, err := exec.CommandContext(ctx, "git", checkArgs...).CombinedOutput()
		cancel()
		if err != nil {
			detail := strings.TrimSpace(string(out))
			if tok != "" {
				detail = strings.ReplaceAll(detail, tok, "•••")
			}
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
		runErr = streamCmd(context.Background(), send, "dokku", args...)
	case req.Image != "":
		send("[check] verifying image exists…")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		out, err := exec.CommandContext(ctx, "docker", "manifest", "inspect", req.Image).CombinedOutput()
		cancel()
		if err != nil {
			finish(false, "image check failed: "+strings.TrimSpace(string(out)))
			return
		}
		send("[check] image found")
		runErr = streamCmd(context.Background(), send, "dokku", "git:from-image", name, req.Image)
	default:
		runErr = streamCmd(context.Background(), send, "dokku", "ps:rebuild", name)
	}
	if runErr != nil {
		finish(false, "deploy exited with an error ("+runErr.Error()+"), see the output above")
		return
	}
	if fixed := ensurePort80(name); fixed != "" {
		send("[gantry] mapped port 80 to the app's port " + fixed + " so it serves on the normal web port")
	}
	finish(true, "")
}

// ensurePort80 maps http:80 to the app's container port. When a Dockerfile
// EXPOSEs a port, dokku publishes e.g. 3000:3000 and the site only answers on
// :3000; visitors expect port 80. Returns the container port when it remapped.
func ensurePort80(app string) string {
	out, err := dokku("ports:report", app, "--ports-map")
	if err != nil {
		return ""
	}
	container := ""
	for _, f := range strings.Fields(out) {
		p := strings.Split(f, ":")
		if len(p) != 3 {
			continue
		}
		if p[1] == "80" || p[1] == "443" {
			return "" // already mapped to a real web port
		}
		if container == "" {
			container = p[2]
		}
	}
	if container == "" {
		return ""
	}
	if _, err := dokku("ports:set", app, "http:80:"+container); err != nil {
		return ""
	}
	return container
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
		"available": latest != "" && newerVersion(latest, version),
	})
}

// newerVersion reports whether latest is strictly newer than current.
// Non-semver values (e.g. a "dev" build) fall back to plain inequality so
// local builds still see updates.
func newerVersion(latest, current string) bool {
	pl, ok1 := verParts(latest)
	pc, ok2 := verParts(current)
	if !ok1 || !ok2 {
		return latest != current
	}
	for i := range 3 {
		if pl[i] != pc[i] {
			return pl[i] > pc[i]
		}
	}
	return false
}

func verParts(v string) ([3]int, bool) {
	var p [3]int
	seg := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(seg) != 3 {
		return p, false
	}
	for i, s := range seg {
		n, err := strconv.Atoi(s)
		if err != nil {
			return p, false
		}
		p[i] = n
	}
	return p, true
}

func handleUpdate(w http.ResponseWriter, r *http.Request) {
	if label := busyWith(); label != "" {
		httpErr(w, 409, "a "+label+" is running right now; updating would interrupt it. Try again when it finishes.")
		return
	}
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
