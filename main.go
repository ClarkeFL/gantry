package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
)

//go:embed all:web/build
var webFS embed.FS

var version = "dev" // -ldflags "-X main.version=..."

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var dataDir = env("GANTRY_DATA", "/var/lib/gantry")

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			initAuth()
			return
		case "version":
			fmt.Println(version)
			return
		}
	}
	if err := loadAuth(); err != nil {
		log.Fatalf("no auth config at %s — run `gantry init` first (%v)", authPath(), err)
	}
	loadMeta()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", handleLogin)
	protected := map[string]http.HandlerFunc{
		"POST /api/logout":               handleLogout,
		"GET /api/me":                    handleMe,
		"POST /api/update":               handleUpdate,
		"GET /api/apps":                  handleApps,
		"GET /api/apps/{name}":           handleAppDetail,
		"POST /api/apps/{name}/env":      handleEnv,
		"POST /api/apps/{name}/category": handleCategory,
		"POST /api/apps/{name}/ps":       handlePs,
		"GET /api/apps/{name}/logs":      handleLogs,
		"POST /api/apps/{name}/deploy":   handleDeploy,
		"PUT /api/apps/{name}/cron":      handleCronPut,
	}
	for p, h := range protected {
		mux.Handle(p, requireAuth(h))
	}
	mux.Handle("/", spaHandler())

	addr := env("GANTRY_ADDR", ":8022")
	log.Printf("gantry %s listening on %s (mock=%v)", version, addr, mockMode)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func spaHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web/build")
	if err != nil {
		log.Fatal(err)
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := sub.Open(p); err == nil {
				f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		b, _ := fs.ReadFile(sub, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
