package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// One-click app templates: a recipe of docker image + persistent storage +
// port mapping + env that gantry wires up on an ordinary app, which then
// deploys through the normal image-deploy path.

type appTemplate struct {
	ID     string            `json:"id"`
	Label  string            `json:"label"`
	Blurb  string            `json:"blurb"`
	Image  string            `json:"image"`
	Mounts []string          `json:"mounts"`
	Env    map[string]string `json:"env,omitempty"`
	Ports  []string          `json:"ports,omitempty"` // "http:80:<container port>"
}

var appTemplates = []appTemplate{
	{
		ID:     "pocketbase",
		Label:  "PocketBase",
		Blurb:  "Backend in a box: SQLite database, auth, file storage and admin UI in one small app.",
		Image:  "ghcr.io/muchobien/pocketbase:latest",
		Mounts: []string{"/pb_data"},
		Ports:  []string{"http:80:8090"},
	},
	{
		ID:     "uptime-kuma",
		Label:  "Uptime Kuma",
		Blurb:  "Self-hosted uptime monitoring with status pages and alerts.",
		Image:  "louislam/uptime-kuma:1",
		Mounts: []string{"/app/data"},
		Ports:  []string{"http:80:3001"},
	},
	{
		ID:     "n8n",
		Label:  "n8n",
		Blurb:  "Workflow automation: connect apps and APIs with a visual editor.",
		Image:  "docker.n8n.io/n8nio/n8n:latest",
		Mounts: []string{"/home/node/.n8n"},
		Env:    map[string]string{"N8N_SECURE_COOKIE": "false"},
		Ports:  []string{"http:80:5678"},
	},
}

func handleTemplatesGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"templates": appTemplates})
}

// handleTemplateCreate creates an app from a template: the frontend then
// sends it through the regular deploy flow.
func handleTemplateCreate(w http.ResponseWriter, r *http.Request) {
	var req struct{ Template, Name, Category, Group string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !appRe.MatchString(req.Name) {
		httpErr(w, 400, "app names must be lowercase letters, digits, . or -")
		return
	}
	var tpl *appTemplate
	for i := range appTemplates {
		if appTemplates[i].ID == req.Template {
			tpl = &appTemplates[i]
			break
		}
	}
	if tpl == nil {
		httpErr(w, 404, "unknown template")
		return
	}
	if out, err := dokku("apps:create", req.Name); err != nil {
		httpErr(w, 500, errText(out, err))
		return
	}
	metaMu.Lock()
	m := getMeta(req.Name)
	m.Category = strings.TrimSpace(req.Category)
	m.Group = strings.TrimSpace(req.Group)
	m.Image = tpl.Image
	err := saveMeta()
	metaMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if len(tpl.Env) > 0 {
		if err := applyEnv(req.Name, tpl.Env, nil); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	for _, p := range tpl.Mounts {
		hostDir, err := ensureStorageDir(storageSlug(req.Name, p))
		if err != nil {
			httpErr(w, 500, "storage setup failed: "+err.Error())
			return
		}
		if _, err := dokku("storage:mount", req.Name, hostDir+":"+p); err != nil {
			httpErr(w, 500, "storage mount failed: "+err.Error())
			return
		}
	}
	if len(tpl.Ports) > 0 {
		args := append([]string{"ports:set", req.Name}, tpl.Ports...)
		if out, err := dokku(args...); err != nil {
			// older dokku spells it proxy:ports-set
			args[0] = "proxy:ports-set"
			if out2, err2 := dokku(args...); err2 != nil {
				httpErr(w, 500, "port setup failed: "+errText(out+out2, err2))
				return
			}
		}
	}
	if m.Category != "" {
		applyProjectEnvToApp(m.Category, req.Name)
	}
	writeJSON(w, map[string]any{"ok": true})
}
