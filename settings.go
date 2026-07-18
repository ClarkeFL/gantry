package main

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	qrcode "github.com/skip2/go-qrcode"
)

type panelSettings struct {
	GitHubUser  string `json:"github_user,omitempty"`
	GitHubToken string `json:"github_token,omitempty"`
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

func otpauthURI() string {
	return fmt.Sprintf("otpauth://totp/gantry?secret=%s&issuer=gantry", auth.TOTPSecret)
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
	writeJSON(w, map[string]any{
		"githubUser":  user,
		"githubToken": masked,
		"totpSecret":  auth.TOTPSecret,
		"totpURI":     otpauthURI(),
	})
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
	salt := make([]byte, 16)
	rand.Read(salt)
	auth.Salt = base64.RawStdEncoding.EncodeToString(salt)
	auth.Hash = hashPassword(req.New, salt)
	if err := saveAuth(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleTOTPRegen(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !verifyPassword(req.Password) {
		httpErr(w, 401, "password is wrong")
		return
	}
	secret := make([]byte, 20)
	rand.Read(secret)
	auth.TOTPSecret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	if err := saveAuth(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"secret": auth.TOTPSecret, "uri": otpauthURI()})
}

func handleTOTPQR(w http.ResponseWriter, r *http.Request) {
	png, err := qrcode.Encode(otpauthURI(), qrcode.Medium, 240)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}
