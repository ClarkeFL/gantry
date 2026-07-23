package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
		case "backup":
			loadSettings()
			if err := runServerBackup(func(l string) { fmt.Println(l) }); err != nil {
				fmt.Fprintln(os.Stderr, "backup failed:", err)
				os.Exit(1)
			}
			return
		case "restore":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: gantry restore <backup.tar.gz>")
				os.Exit(1)
			}
			if err := restoreBackup(os.Args[2]); err != nil {
				fmt.Fprintln(os.Stderr, "restore failed:", err)
				os.Exit(1)
			}
			return
		}
	}
	if err := loadAuth(); err != nil {
		log.Printf("no account yet, open the panel to register (looked in %s)", authPath())
	}
	loadMeta()
	loadSettings()
	loadSessions()
	if mockMode {
		loadMockState()
	} else {
		// nightly disk reclaim, old deploy images fill small VPSes within months
		prune := "# managed by gantry, reclaims disk from old deploy images\n" +
			"30 4 * * * root docker image prune -af --filter \"until=168h\" >/dev/null 2>&1; docker container prune -f >/dev/null 2>&1\n"
		os.WriteFile(filepath.Join(cronDir, "gantry-prune"), []byte(prune), 0o644)
	}
	startStatsSampler()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/register", handleRegister)
	mux.HandleFunc("POST /api/login", handleLogin)
	mux.HandleFunc("POST /api/login/mfa", handleLoginMFA)
	mux.HandleFunc("GET /api/me", handleMe)
	protected := map[string]http.HandlerFunc{
		"POST /api/logout":                handleLogout,
		"POST /api/update":                handleUpdate,
		"GET /api/update/check":           handleUpdateCheck,
		"GET /api/stats":                  handleStats,
		"GET /api/domains":                handleDomains,
		"POST /api/apps":                  handleCreateApp,
		"POST /api/services":              handleCreateService,
		"POST /api/projects":              handleProjectCreate,
		"DELETE /api/projects":            handleProjectDelete,
		"PUT /api/projects/order":         handleProjectOrder,
		"GET /api/projects/{name}/env":    handleProjectEnvGet,
		"POST /api/projects/{name}/env":   handleProjectEnvSet,
		"GET /api/services":               handleServicesGet,
		"DELETE /api/apps/{name}":         handleAppDestroy,
		"DELETE /api/services":            handleServiceDestroy,
		"POST /api/services/category":     handleServiceCategorySet,
		"POST /api/services/plugins":      handleInstallPlugin,
		"GET /api/settings":               handleSettingsGet,
		"POST /api/settings/github":       handleGitHubSet,
		"POST /api/settings/letsencrypt":  handleLEEmail,
		"POST /api/settings/registry":     handleRegistryAdd,
		"POST /api/settings/session":      handleSessionDays,
		"POST /api/settings/tokens":       handleTokenCreate,
		"DELETE /api/settings/tokens":     handleTokenDelete,
		"POST /api/apps/{name}/domains":   handleDomainsMod,
		"PUT /api/apps/{name}/source":     handleSourceSet,
		"POST /api/apps/{name}/ssl":         handleSSL,
		"POST /api/apps/{name}/maintenance": handleMaintenance,
		"POST /api/apps/{name}/storage":     handleStorageMod,
		"GET /api/maintenance/preview":      handleMaintenancePreview,
		"POST /api/settings/password":     handleChangePassword,
		"POST /api/settings/totp/setup":   handleTOTPSetup,
		"POST /api/settings/totp/verify":  handleTOTPVerify,
		"POST /api/settings/totp/disable": handleTOTPDisable,
		"GET /api/settings/totp.png":      handleTOTPQR,
		"GET /api/apps":                  handleApps,
		"GET /api/apps/{name}":           handleAppDetail,
		"POST /api/apps/{name}/env":      handleEnv,
		"POST /api/apps/{name}/category": handleCategory,
		"POST /api/apps/{name}/ps":       handlePs,
		"GET /api/apps/{name}/logs":        handleLogs,
		"GET /api/apps/{name}/logs/deploy": handleDeployLog,
		"GET /api/apps/{name}/deploys":     handleDeploys,
		"POST /api/apps/{name}/deploy":   handleDeploy,
		"PUT /api/apps/{name}/cron":      handleCronPut,
		"POST /api/services/link":            handleServiceLink,
		"GET /api/backups":                   handleBackups,
		"POST /api/services/backup":          handleServiceBackup,
		"POST /api/services/backup/schedule": handleBackupSchedule,
		"POST /api/backup/server":            handleServerBackup,
		"POST /api/backup/server/schedule":   handleServerBackupSchedule,
		"GET /api/backup/list":               handleBackupArchiveList,
		"GET /api/backup/apps":               handleBackupArchiveApps,
		"POST /api/apps/{name}/restore":      handleAppRestore,
		"POST /api/settings/s3":              handleS3Set,
		"POST /api/settings/webhook":         handleWebhookSet,
		"POST /api/settings/timezone":        handleDisplayTZSet,
		"GET /api/audit":                     handleAudit,
	}
	for p, h := range protected {
		mux.Handle(p, requireAuth(h))
	}
	mux.Handle("/", spaHandler())

	addr := env("GANTRY_ADDR", ":8022")
	useTLS := env("GANTRY_TLS", map[bool]string{true: "0", false: "1"}[mockMode]) == "1"
	if useTLS {
		log.Printf("gantry %s listening on %s (mock=%v)", version, addr, mockMode)
		log.Fatal(serveDual(addr, mux))
	}
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
