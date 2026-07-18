package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	qrcode "github.com/skip2/go-qrcode"
)

type registryCred struct {
	Server string `json:"server"`
	User   string `json:"user"`
}

type panelSettings struct {
	GitHubUser  string         `json:"github_user,omitempty"`
	GitHubToken string         `json:"github_token,omitempty"`
	LEEmail     string         `json:"letsencrypt_email,omitempty"`
	Categories  []string       `json:"categories,omitempty"`
	Registries  []registryCred `json:"registries,omitempty"` // display only — docker stores the creds
}

var (
	settingsMu sync.Mutex
	settings   panelSettings
)

func settingsPath() string { return filepath.Join(dataDir, "settings.json") }

func loadSettings() {
	if b, err := os.ReadFile(settingsPath()); err == nil {
		json.Unmarshal(b, &settings)
	}
}

func saveSettings() error { // callers hold settingsMu
	b, _ := json.MarshalIndent(settings, "", "  ")
	return os.WriteFile(settingsPath(), b, 0o600)
}

func githubToken() string {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return settings.GitHubToken
}

func otpauthURI(secret string) string {
	return fmt.Sprintf("otpauth://totp/gantry?secret=%s&issuer=gantry", secret)
}

func handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	settingsMu.Lock()
	user, tok := settings.GitHubUser, settings.GitHubToken
	settingsMu.Unlock()
	masked := ""
	if n := len(tok); n > 4 {
		masked = "••••••••" + tok[n-4:]
	} else if n > 0 {
		masked = "••••••••"
	}
	settingsMu.Lock()
	leEmail := settings.LEEmail
	registries := append([]registryCred{}, settings.Registries...)
	settingsMu.Unlock()
	out := map[string]any{
		"githubUser":  user,
		"githubToken": masked,
		"leEmail":     leEmail,
		"registries":  registries,
		"email":       auth.Email,
		"totpEnabled": auth.TOTPSecret != "",
		"totpPending": auth.PendingTOTP != "",
	}
	if auth.PendingTOTP != "" {
		out["pendingSecret"] = auth.PendingTOTP
	}
	writeJSON(w, out)
}

// handleTOTPSetup stages a new secret; it only becomes active once a code is verified.
func handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	auth.PendingTOTP = newTOTPSecret()
	if err := saveAuth(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"secret": auth.PendingTOTP, "uri": otpauthURI(auth.PendingTOTP)})
}

func handleTOTPVerify(w http.ResponseWriter, r *http.Request) {
	var req struct{ Code string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if auth.PendingTOTP == "" {
		httpErr(w, 400, "no 2FA setup in progress")
		return
	}
	if !codeValid(auth.PendingTOTP, strings.TrimSpace(req.Code)) {
		httpErr(w, 401, "wrong code — try the next one from your app")
		return
	}
	auth.TOTPSecret, auth.PendingTOTP = auth.PendingTOTP, ""
	if err := saveAuth(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !verifyPassword(req.Password) {
		httpErr(w, 401, "password is wrong")
		return
	}
	auth.TOTPSecret, auth.PendingTOTP = "", ""
	if err := saveAuth(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleRegistryAdd logs the server's docker into a registry for private image pulls.
func handleRegistryAdd(w http.ResponseWriter, r *http.Request) {
	var req struct{ Server, User, Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	req.Server = strings.TrimSpace(req.Server)
	if req.Server == "" {
		req.Server = "docker.io"
	}
	req.User = strings.TrimSpace(req.User)
	if req.User == "" || req.Password == "" {
		httpErr(w, 400, "username and password are required")
		return
	}
	if out, err := dokku("registry:login", req.Server, req.User, req.Password); err != nil {
		httpErr(w, 401, "registry login failed: "+out)
		return
	}
	settingsMu.Lock()
	found := false
	for i, c := range settings.Registries {
		if c.Server == req.Server {
			settings.Registries[i].User = req.User
			found = true
			break
		}
	}
	if !found {
		settings.Registries = append(settings.Registries, registryCred{req.Server, req.User})
	}
	err := saveSettings()
	registries := append([]registryCred{}, settings.Registries...)
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "registries": registries})
}

func handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpErr(w, 400, "category name required")
		return
	}
	settingsMu.Lock()
	found := false
	for _, c := range settings.Categories {
		if strings.EqualFold(c, req.Name) {
			found = true
			break
		}
	}
	if !found {
		settings.Categories = append(settings.Categories, req.Name)
	}
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleLEEmail(w http.ResponseWriter, r *http.Request) {
	var req struct{ Email string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		httpErr(w, 400, "enter a valid email")
		return
	}
	settingsMu.Lock()
	settings.LEEmail = req.Email
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !mockMode {
		if out, err := dokku("letsencrypt:set", "--global", "email", req.Email); err != nil {
			httpErr(w, 500, "saved, but dokku rejected it: "+out+" (is the letsencrypt plugin installed?)")
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleGitHubSet(w http.ResponseWriter, r *http.Request) {
	var req struct{ User, Token string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	settingsMu.Lock()
	settings.GitHubUser, settings.GitHubToken = req.User, req.Token
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	registry := "skipped"
	if req.Token != "" && req.User != "" && !mockMode {
		if out, err := dokku("registry:login", "ghcr.io", req.User, req.Token); err != nil {
			registry = "ghcr.io login failed: " + out
		} else {
			registry = "logged in to ghcr.io"
		}
	}
	writeJSON(w, map[string]any{"ok": true, "registry": registry})
}

func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Current, New string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !verifyPassword(req.Current) {
		httpErr(w, 401, "current password is wrong")
		return
	}
	if len(req.New) < 8 {
		httpErr(w, 400, "new password must be at least 8 characters")
		return
	}
	setPassword(req.New)
	if err := saveAuth(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleTOTPQR shows the QR only while a setup is pending — the active secret is never re-displayed.
func handleTOTPQR(w http.ResponseWriter, r *http.Request) {
	if auth.PendingTOTP == "" {
		httpErr(w, 404, "no 2FA setup in progress")
		return
	}
	png, err := qrcode.Encode(otpauthURI(auth.PendingTOTP), qrcode.Medium, 240)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}
