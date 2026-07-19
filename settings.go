package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	Registries  []registryCred `json:"registries,omitempty"` // display only, docker stores the creds

	DBCategories []string          `json:"db_categories,omitempty"`
	DBCategory   map[string]string `json:"db_category,omitempty"` // "postgres/main-db" -> category

	SessionDays int `json:"session_days,omitempty"` // 0 = default 7

	APITokens []apiToken `json:"api_tokens,omitempty"`

	// S3-compatible storage for database backups (dokku <plugin>:backup)
	S3Bucket   string `json:"s3_bucket,omitempty"`
	S3Region   string `json:"s3_region,omitempty"`
	S3Key      string `json:"s3_key,omitempty"`
	S3Secret   string `json:"s3_secret,omitempty"`
	S3Endpoint string `json:"s3_endpoint,omitempty"` // blank = AWS

	AlertWebhook string `json:"alert_webhook,omitempty"` // Slack/Discord-compatible
	BackupKeep   int    `json:"backup_keep,omitempty"`   // server backups to retain, 0 = default 7
}

type apiToken struct {
	Name    string `json:"name"`
	Hash    string `json:"hash"` // sha256 hex, the token itself is shown once and never stored
	Created string `json:"created"`
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
	sessionDays := settings.SessionDays
	settingsMu.Unlock()
	if sessionDays == 0 {
		sessionDays = 7
	}
	settingsMu.Lock()
	tokens := make([]map[string]string, 0, len(settings.APITokens))
	for _, t := range settings.APITokens {
		tokens = append(tokens, map[string]string{"name": t.Name, "created": t.Created})
	}
	settingsMu.Unlock()
	settingsMu.Lock()
	s3 := map[string]any{
		"bucket":   settings.S3Bucket,
		"region":   settings.S3Region,
		"endpoint": settings.S3Endpoint,
		"keySet":   settings.S3Key != "" && settings.S3Secret != "",
	}
	webhook := settings.AlertWebhook
	settingsMu.Unlock()
	out := map[string]any{
		"githubUser":   user,
		"githubToken":  masked,
		"leEmail":      leEmail,
		"registries":   registries,
		"sessionDays":  sessionDays,
		"tokens":       tokens,
		"s3":           s3,
		"alertWebhook": webhook,
		"email":        auth.Email,
		"totpEnabled":  auth.TOTPSecret != "",
		"totpPending":  auth.PendingTOTP != "",
		"recoveryLeft": len(auth.Recovery),
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
		httpErr(w, 401, "wrong code, try the next one from your app")
		return
	}
	auth.TOTPSecret, auth.PendingTOTP = auth.PendingTOTP, ""
	// fresh one-time recovery codes, shown once, stored hashed
	codes := make([]string, 8)
	auth.Recovery = nil
	for i := range codes {
		b := make([]byte, 4)
		rand.Read(b)
		c := hex.EncodeToString(b)
		codes[i] = c[:4] + "-" + c[4:]
		sum := sha256.Sum256([]byte(c))
		auth.Recovery = append(auth.Recovery, hex.EncodeToString(sum[:]))
	}
	if err := saveAuth(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "recovery": codes})
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
	auth.TOTPSecret, auth.PendingTOTP, auth.Recovery = "", "", nil
	if err := saveAuth(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleS3Set(w http.ResponseWriter, r *http.Request) {
	var req struct{ Bucket, Region, Key, Secret, Endpoint string }
	json.NewDecoder(r.Body).Decode(&req)
	settingsMu.Lock()
	settings.S3Bucket = strings.TrimSpace(req.Bucket)
	settings.S3Region = strings.TrimSpace(req.Region)
	settings.S3Endpoint = strings.TrimSpace(req.Endpoint)
	if strings.TrimSpace(req.Key) != "" {
		settings.S3Key = strings.TrimSpace(req.Key)
	}
	if strings.TrimSpace(req.Secret) != "" {
		settings.S3Secret = strings.TrimSpace(req.Secret)
	}
	saveSettings()
	settingsMu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func handleWebhookSet(w http.ResponseWriter, r *http.Request) {
	var req struct{ URL string }
	json.NewDecoder(r.Body).Decode(&req)
	settingsMu.Lock()
	settings.AlertWebhook = strings.TrimSpace(req.URL)
	saveSettings()
	settingsMu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

// notifyWebhook fires-and-forgets a message to the configured webhook.
// The payload carries both "text" (Slack) and "content" (Discord).
func notifyWebhook(msg string) {
	settingsMu.Lock()
	url := settings.AlertWebhook
	settingsMu.Unlock()
	if url == "" {
		return
	}
	b, _ := json.Marshal(map[string]string{"text": msg, "content": msg})
	client := &http.Client{Timeout: 10 * time.Second}
	client.Post(url, "application/json", strings.NewReader(string(b)))
}

// --- API tokens: Bearer auth for agents/scripts; full API except /api/settings ---

func handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	json.NewDecoder(r.Body).Decode(&req)
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpErr(w, 400, "token name required")
		return
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := "gantry_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	settingsMu.Lock()
	for _, t := range settings.APITokens {
		if t.Name == req.Name {
			settingsMu.Unlock()
			httpErr(w, 409, "a token with that name already exists")
			return
		}
	}
	settings.APITokens = append(settings.APITokens, apiToken{
		Name: req.Name, Hash: hex.EncodeToString(sum[:]), Created: time.Now().Format("2006-01-02"),
	})
	saveSettings()
	settingsMu.Unlock()
	writeJSON(w, map[string]string{"token": token})
}

func handleTokenDelete(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	json.NewDecoder(r.Body).Decode(&req)
	settingsMu.Lock()
	kept := settings.APITokens[:0]
	for _, t := range settings.APITokens {
		if t.Name != req.Name {
			kept = append(kept, t)
		}
	}
	settings.APITokens = kept
	saveSettings()
	settingsMu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func tokenName(r *http.Request) (string, bool) {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return "", false
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(bearer)))
	h := hex.EncodeToString(sum[:])
	settingsMu.Lock()
	defer settingsMu.Unlock()
	for _, t := range settings.APITokens {
		if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(h)) == 1 {
			return t.Name, true
		}
	}
	return "", false
}

func handleSessionDays(w http.ResponseWriter, r *http.Request) {
	var req struct{ Days int }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if req.Days < 1 || req.Days > 90 {
		httpErr(w, 400, "days must be between 1 and 90")
		return
	}
	settingsMu.Lock()
	settings.SessionDays = req.Days
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
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

func handleDBCategoryCreate(w http.ResponseWriter, r *http.Request) {
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
	for _, c := range settings.DBCategories {
		if strings.EqualFold(c, req.Name) {
			found = true
			break
		}
	}
	if !found {
		settings.DBCategories = append(settings.DBCategories, req.Name)
	}
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleDBCategoryDelete(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	settingsMu.Lock()
	kept := settings.DBCategories[:0]
	for _, c := range settings.DBCategories {
		if !strings.EqualFold(c, req.Name) {
			kept = append(kept, c)
		}
	}
	settings.DBCategories = kept
	for k, v := range settings.DBCategory {
		if strings.EqualFold(v, req.Name) {
			delete(settings.DBCategory, k)
		}
	}
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleServiceCategorySet(w http.ResponseWriter, r *http.Request) {
	var req struct{ Type, Name, Category string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !serviceTypes[req.Type] || !appRe.MatchString(req.Name) {
		httpErr(w, 400, "bad service type or name")
		return
	}
	settingsMu.Lock()
	if settings.DBCategory == nil {
		settings.DBCategory = map[string]string{}
	}
	key := req.Type + "/" + req.Name
	if c := strings.TrimSpace(req.Category); c == "" {
		delete(settings.DBCategory, key)
	} else {
		settings.DBCategory[key] = c
	}
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func readNames(r *http.Request) ([]string, bool) {
	var req struct{ Names []string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, false
	}
	out := []string{}
	seen := map[string]bool{}
	for _, n := range req.Names {
		n = strings.TrimSpace(n)
		if n != "" && !seen[strings.ToLower(n)] { // 'Uncategorised' allowed: its position is orderable
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
	}
	return out, true
}

func handleCategoryOrder(w http.ResponseWriter, r *http.Request) {
	names, ok := readNames(r)
	if !ok {
		httpErr(w, 400, "bad request")
		return
	}
	settingsMu.Lock()
	settings.Categories = names
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleDBCategoryOrder(w http.ResponseWriter, r *http.Request) {
	names, ok := readNames(r)
	if !ok {
		httpErr(w, 400, "bad request")
		return
	}
	settingsMu.Lock()
	settings.DBCategories = names
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleCategoryDelete(w http.ResponseWriter, r *http.Request) {
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
	kept := settings.Categories[:0]
	for _, c := range settings.Categories {
		if !strings.EqualFold(c, req.Name) {
			kept = append(kept, c)
		}
	}
	settings.Categories = kept
	err := saveSettings()
	settingsMu.Unlock()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	// apps in the deleted category fall back to Uncategorised
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

// handleTOTPQR shows the QR only while a setup is pending, the active secret is never re-displayed.
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
