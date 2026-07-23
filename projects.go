package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// Projects group apps and database services and carry a shared env that
// member apps inherit. Membership reuses the existing storage: apps via
// meta.json "category", services via settings.DBCategory. Only the shared
// env and the unified name list are new.

// projectEnvApply computes what to set/unset on one app when a project's
// env changes from oldProj to newProj. An app-local override (value that
// differs from what the project last applied) is never touched.
func projectEnvApply(oldProj, newProj, appEnv map[string]string) (set map[string]string, unset []string) {
	set = map[string]string{}
	for k, v := range newProj {
		cur, has := appEnv[k]
		if has && cur != oldProj[k] {
			continue // app overrode this key, leave it
		}
		if !has || cur != v {
			set[k] = v
		}
	}
	for k := range oldProj {
		if _, still := newProj[k]; still {
			continue
		}
		if cur, has := appEnv[k]; has && cur == oldProj[k] {
			unset = append(unset, k)
		}
	}
	sort.Strings(unset)
	return set, unset
}

// applyEnv runs the dokku config calls for one app. Shared by the per-app
// env handler and the project fan-out.
func applyEnv(app string, set map[string]string, unset []string) error {
	if len(set) > 0 {
		args := []string{"config:set", "--no-restart", app}
		keys := make([]string, 0, len(set))
		for k := range set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, k+"="+set[k])
		}
		if _, err := dokku(args...); err != nil {
			return err
		}
	}
	if len(unset) > 0 {
		args := append([]string{"config:unset", "--no-restart", app}, unset...)
		if _, err := dokku(args...); err != nil {
			return err
		}
	}
	return nil
}

func appEnvVars(app string) map[string]string {
	envVars := map[string]string{}
	if out, err := dokku("config:export", "--format", "json", app); err == nil {
		json.Unmarshal([]byte(out), &envVars)
	}
	return envVars
}

// projectApps lists app names whose meta category matches the project.
func projectApps(project string) []string {
	metaMu.Lock()
	defer metaMu.Unlock()
	out := []string{}
	for name, m := range meta {
		if strings.EqualFold(m.Category, project) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// applyProjectEnvToApp is the "app joins a project" hook: every project key
// the app doesn't already define gets set. Existing app values win.
func applyProjectEnvToApp(project, app string) {
	settingsMu.Lock()
	proj := settings.ProjectEnv[project]
	settingsMu.Unlock()
	if len(proj) == 0 {
		return
	}
	appEnv := appEnvVars(app)
	set := map[string]string{}
	for k, v := range proj {
		if _, has := appEnv[k]; !has {
			set[k] = v
		}
	}
	applyEnv(app, set, nil)
}

func projectName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		httpErr(w, 400, "project name required")
		return "", false
	}
	return name, true
}

func handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpErr(w, 400, "project name required")
		return
	}
	settingsMu.Lock()
	found := false
	for _, c := range settings.Projects {
		if strings.EqualFold(c, req.Name) {
			found = true
			break
		}
	}
	if !found {
		settings.Projects = append(settings.Projects, req.Name)
	}
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpErr(w, 400, "project name required")
		return
	}
	settingsMu.Lock()
	kept := settings.Projects[:0]
	for _, c := range settings.Projects {
		if !strings.EqualFold(c, req.Name) {
			kept = append(kept, c)
		}
	}
	settings.Projects = kept
	for k, v := range settings.DBCategory {
		if strings.EqualFold(v, req.Name) {
			delete(settings.DBCategory, k)
		}
	}
	delete(settings.ProjectEnv, req.Name)
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// apps in the deleted project fall back to Unassigned; their env is left
	// untouched so nothing breaks
	metaMu.Lock()
	changed := false
	for _, m := range meta {
		if strings.EqualFold(m.Category, req.Name) {
			m.Category = ""
			changed = true
		}
	}
	if changed {
		err = saveMeta()
	}
	metaMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleProjectOrder(w http.ResponseWriter, r *http.Request) {
	names, ok := readNames(r)
	if !ok {
		httpErr(w, 400, "bad request")
		return
	}
	settingsMu.Lock()
	settings.Projects = names
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleProjectEnvGet(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
	if !ok {
		return
	}
	settingsMu.Lock()
	env := map[string]string{}
	for k, v := range settings.ProjectEnv[name] {
		env[k] = v
	}
	settingsMu.Unlock()
	writeJSON(w, map[string]any{"env": env})
}

// handleProjectEnvSet updates the shared env and fans the change out to
// every member app, respecting app-local overrides.
func handleProjectEnvSet(w http.ResponseWriter, r *http.Request) {
	name, ok := projectName(w, r)
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
	settingsMu.Lock()
	oldProj := settings.ProjectEnv[name]
	newProj := map[string]string{}
	for k, v := range oldProj {
		newProj[k] = v
	}
	for k, v := range req.Set {
		newProj[k] = v
	}
	for _, k := range req.Unset {
		delete(newProj, k)
	}
	if settings.ProjectEnv == nil {
		settings.ProjectEnv = map[string]map[string]string{}
	}
	settings.ProjectEnv[name] = newProj
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	apps := projectApps(name)
	failed := []string{}
	for _, app := range apps {
		set, unset := projectEnvApply(oldProj, newProj, appEnvVars(app))
		if err := applyEnv(app, set, unset); err != nil {
			failed = append(failed, app+": "+err.Error())
			continue
		}
		if req.Restart {
			if _, err := dokku("ps:restart", app); err != nil {
				failed = append(failed, app+": restart: "+err.Error())
			}
		}
	}
	if len(failed) > 0 {
		httpErr(w, 500, "saved, but applying to some apps failed: "+strings.Join(failed, "; "))
		return
	}
	writeJSON(w, map[string]any{"ok": true, "applied": len(apps)})
}
