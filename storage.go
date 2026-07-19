package main

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"
)

// Persistent storage via dokku storage:mount. Gantry manages the host side:
// each mount gets an auto-created directory under dokku's storage root named
// <app>-<slug-of-container-path>, so users only ever think in container paths.

type mount struct {
	HostDir       string `json:"hostDir"`
	ContainerPath string `json:"containerPath"`
}

func listMounts(app string) []mount {
	out, err := dokku("storage:list", app)
	if err != nil {
		return []mount{}
	}
	mounts := []mount{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "=") || strings.HasPrefix(line, "!") {
			continue
		}
		host, container, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		mounts = append(mounts, mount{host, container})
	}
	return mounts
}

func storageSlug(app, containerPath string) string {
	s := strings.Trim(containerPath, "/")
	s = strings.ReplaceAll(s, "/", "-")
	return app + "-" + s
}

// ensureStorageDir creates the host directory with dokku's expected ownership
// and returns its path (parsed from the output, with the standard location as
// fallback since the storage root can be moved).
func ensureStorageDir(slug string) (string, error) {
	out, err := dokku("storage:ensure-directory", slug)
	if err != nil {
		return "", err
	}
	hostDir := "/var/lib/dokku/data/storage/" + slug
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "/") && strings.HasSuffix(f, slug) {
			hostDir = f
		}
	}
	return hostDir, nil
}

func handleStorageMod(w http.ResponseWriter, r *http.Request) {
	name, ok := appName(w, r)
	if !ok {
		return
	}
	var req struct {
		Path   string `json:"path"`
		Remove bool   `json:"remove"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	p := path.Clean(strings.TrimSpace(req.Path))
	if !strings.HasPrefix(p, "/") || p == "/" || strings.ContainsAny(p, ": \t") {
		httpErr(w, 400, "enter an absolute folder path inside the container, like /data/uploads")
		return
	}
	if req.Remove {
		for _, m := range listMounts(name) {
			if m.ContainerPath == p {
				if _, err := dokku("storage:unmount", name, m.HostDir+":"+m.ContainerPath); err != nil {
					httpErr(w, 500, err.Error())
					return
				}
			}
		}
	} else {
		for _, m := range listMounts(name) {
			if m.ContainerPath == p {
				httpErr(w, 400, "that folder is already attached")
				return
			}
		}
		hostDir, err := ensureStorageDir(storageSlug(name, p))
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		if _, err := dokku("storage:mount", name, hostDir+":"+p); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	// mounts only take effect on the next container start
	restarted := false
	if running, _ := dokku("ps:report", name, "--running"); running == "true" {
		dokku("ps:restart", name)
		restarted = true
	}
	writeJSON(w, map[string]any{"mounts": listMounts(name), "restarted": restarted})
}
