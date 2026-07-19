package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
)

// Maintenance mode uses the official dokku-maintenance plugin: nginx serves a
// static page instead of the app. We embed three ready-made pages, pipe the
// chosen one in as a tarball via maintenance:custom-page, then maintenance:on.

const maintenanceHead = `<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="refresh" content="30"><title>{{app}} | be right back</title>`

var maintenanceTemplates = map[string]string{
	"clean": maintenanceHead + `<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#f8fafc;color:#0f172a;font-family:system-ui,sans-serif;text-align:center}
.dot{width:10px;height:10px;border-radius:50%;background:#f59e0b;margin:0 auto 24px;animation:p 1.6s ease-in-out infinite}
@keyframes p{50%{opacity:.25}}h1{font-size:28px;margin:0 0 10px;font-weight:600}p{margin:0;color:#64748b;font-size:15px;line-height:1.6}
</style><div><div class="dot"></div><h1>We&rsquo;ll be right back</h1><p>{{app}} is briefly down for maintenance.<br>This page refreshes automatically.</p></div>`,

	"dark": maintenanceHead + `<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#0d1117;color:#e6edf3;font-family:system-ui,sans-serif;text-align:center}
.ring{width:36px;height:36px;border:3px solid #21262d;border-top-color:#58a6ff;border-radius:50%;margin:0 auto 24px;animation:s 1.2s linear infinite}
@keyframes s{to{transform:rotate(360deg)}}h1{font-size:28px;margin:0 0 10px;font-weight:600}p{margin:0;color:#8b949e;font-size:15px;line-height:1.6}
</style><div><div class="ring"></div><h1>Down for maintenance</h1><p>{{app}} will be back shortly.<br>No need to refresh, this page retries on its own.</p></div>`,

	"construction": maintenanceHead + `<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#fffbeb;color:#1c1917;font-family:system-ui,sans-serif;text-align:center}
.bar{position:fixed;top:0;left:0;right:0;height:10px;background:repeating-linear-gradient(45deg,#f59e0b 0 14px,#1c1917 14px 28px)}
.e{font-size:44px;margin-bottom:16px}h1{font-size:28px;margin:0 0 10px;font-weight:600}p{margin:0;color:#78716c;font-size:15px;line-height:1.6}
</style><div class="bar"></div><div><div class="e">&#128679;</div><h1>Under construction</h1><p>{{app}} is getting an upgrade.<br>Check back in a little while.</p></div>`,
}

func maintenancePage(tpl, app string) string {
	html, ok := maintenanceTemplates[tpl]
	if !ok {
		html = maintenanceTemplates["clean"]
	}
	return strings.ReplaceAll(html, "{{app}}", app)
}

// setMaintenancePage tars the chosen template and pipes it to
// `dokku maintenance:custom-page <app>` (the plugin's stdin format).
func setMaintenancePage(name, tpl string) error {
	if mockMode {
		return nil
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	html := []byte(maintenancePage(tpl, name))
	tw.WriteHeader(&tar.Header{Name: "maintenance.html", Mode: 0o644, Size: int64(len(html))})
	tw.Write(html)
	tw.Close()
	cmd := exec.Command("dokku", "maintenance:custom-page", name)
	cmd.Stdin = &buf
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("maintenance:custom-page: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// maintenanceAll returns app -> maintenance-enabled for every app in one
// dokku call. Empty map when the plugin is missing.
func maintenanceAll() map[string]bool {
	out := map[string]bool{}
	if mockMode {
		mockMu.Lock()
		defer mockMu.Unlock()
		for k, v := range mockMaintenance {
			out[k] = v
		}
		return out
	}
	txt, err := dokku("maintenance:report")
	if err != nil {
		return out
	}
	cur := ""
	for _, line := range strings.Split(txt, "\n") {
		if f := strings.Fields(line); strings.HasPrefix(line, "=====>") && len(f) >= 2 {
			cur = f[1]
			continue
		}
		if l := strings.ToLower(line); cur != "" && strings.Contains(l, "enabled") {
			out[cur] = strings.Contains(l, "true")
		}
	}
	return out
}

func handleMaintenance(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct {
		On       bool   `json:"on"`
		Template string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad json")
		return
	}
	if req.On {
		if req.Template == "" {
			req.Template = "clean"
		}
		if _, ok := maintenanceTemplates[req.Template]; !ok {
			httpErr(w, 400, "unknown template")
			return
		}
		if err := setMaintenancePage(name, req.Template); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if _, err := dokku("maintenance:on", name); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		metaMu.Lock()
		getMeta(name).MaintenanceTpl = req.Template
		saveMeta()
		metaMu.Unlock()
	} else if _, err := dokku("maintenance:off", name); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"on": req.On})
}

// handleMaintenancePreview renders a template in the browser so the user can
// see it before enabling. ?template=clean|dark|construction&app=name
func handleMaintenancePreview(w http.ResponseWriter, r *http.Request) {
	app := r.URL.Query().Get("app")
	if app == "" || !appRe.MatchString(app) {
		app = "your app"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, maintenancePage(r.URL.Query().Get("template"), app))
}
