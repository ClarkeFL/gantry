package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/term"
)

type authConfig struct {
	Email       string `json:"email,omitempty"`
	Salt        string `json:"salt"`
	Hash        string `json:"hash"`
	TOTPSecret  string `json:"totp_secret,omitempty"`
	PendingTOTP string `json:"pending_totp,omitempty"`
}

var (
	authMu sync.Mutex
	auth   authConfig
)

func authPath() string { return filepath.Join(dataDir, "auth.json") }

func loadAuth() error {
	b, err := os.ReadFile(authPath())
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &auth)
}

func saveAuth() error {
	os.MkdirAll(dataDir, 0o755)
	b, _ := json.MarshalIndent(auth, "", "  ")
	return os.WriteFile(authPath(), b, 0o600)
}

func authExists() bool { return auth.Hash != "" }

func hashPassword(pw string, salt []byte) string {
	return base64.RawStdEncoding.EncodeToString(argon2.IDKey([]byte(pw), salt, 1, 64*1024, 4, 32))
}

func verifyPassword(pw string) bool {
	salt, _ := base64.RawStdEncoding.DecodeString(auth.Salt)
	return subtle.ConstantTimeCompare([]byte(hashPassword(pw, salt)), []byte(auth.Hash)) == 1
}

func setPassword(pw string) {
	salt := make([]byte, 16)
	rand.Read(salt)
	auth.Salt = base64.RawStdEncoding.EncodeToString(salt)
	auth.Hash = hashPassword(pw, salt)
}

func newTOTPSecret() string {
	secret := make([]byte, 20)
	rand.Read(secret)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

// initAuth is the CLI fallback/reset path; normal setup happens in the browser.
func initAuth() {
	read := func(prompt string, secret bool) string {
		fmt.Print(prompt)
		if secret && term.IsTerminal(int(os.Stdin.Fd())) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			if err != nil {
				fmt.Println("\nerror:", err)
				os.Exit(1)
			}
			fmt.Println()
			return string(b)
		}
		sc := bufio.NewScanner(os.Stdin)
		sc.Scan()
		return strings.TrimSpace(sc.Text())
	}
	email := read("Admin email: ", false)
	pw := read("Admin password (min 8 chars): ", true)
	if len(pw) < 8 || !strings.Contains(email, "@") {
		fmt.Println("need a valid email and a password of at least 8 characters")
		os.Exit(1)
	}
	auth = authConfig{Email: strings.ToLower(email)}
	setPassword(pw)
	if err := saveAuth(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	fmt.Println("\nAccount created. Enable 2FA in the panel: Settings → Two-factor authentication.")
}

// --- TOTP (RFC 6238, stdlib only) ---

func totp(secret string, t time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(t.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0xf
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000)
}

func codeValid(secret, code string) bool {
	now := time.Now()
	ok := false
	for _, dt := range []time.Duration{0, -30 * time.Second, 30 * time.Second} {
		if subtle.ConstantTimeCompare([]byte(code), []byte(totp(secret, now.Add(dt)))) == 1 {
			ok = true
		}
	}
	return ok
}

// --- sessions (in-memory; a restart logs everyone out, which is fine) ---

const sessionTTL = 7 * 24 * time.Hour

var (
	sessMu   sync.Mutex
	sessions = map[string]time.Time{}
)

func randToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func newSession() string {
	tok := randToken()
	sessMu.Lock()
	sessions[tok] = time.Now().Add(sessionTTL)
	sessMu.Unlock()
	return tok
}

func setSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: "gantry_s", Value: newSession(), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(sessionTTL.Seconds()),
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
}

func sessionValid(r *http.Request) bool {
	c, err := r.Cookie("gantry_s")
	if err != nil {
		return false
	}
	sessMu.Lock()
	defer sessMu.Unlock()
	exp, ok := sessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(sessions, c.Value)
		return false
	}
	return true
}

func requireAuth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sessionValid(r) {
			httpErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	})
}

// --- login rate limiting: 10 attempts / 15 min per IP ---

var (
	rlMu       sync.Mutex
	rlAttempts = map[string][]time.Time{}
)

func rateLimited(ip string) bool {
	rlMu.Lock()
	defer rlMu.Unlock()
	cut := time.Now().Add(-15 * time.Minute)
	kept := rlAttempts[ip][:0]
	for _, t := range rlAttempts[ip] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, time.Now())
	rlAttempts[ip] = kept
	return len(kept) > 10
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- pending MFA tokens: password accepted, waiting on the 2FA code ---

var (
	mfaMu     sync.Mutex
	mfaTokens = map[string]time.Time{}
)

func newMFAToken() string {
	tok := randToken()
	mfaMu.Lock()
	mfaTokens[tok] = time.Now().Add(5 * time.Minute)
	mfaMu.Unlock()
	return tok
}

func takeMFAToken(tok string) bool {
	mfaMu.Lock()
	defer mfaMu.Unlock()
	exp, ok := mfaTokens[tok]
	if !ok || time.Now().After(exp) {
		return false
	}
	return true
}

func burnMFAToken(tok string) {
	mfaMu.Lock()
	delete(mfaTokens, tok)
	mfaMu.Unlock()
}

// --- handlers ---

func handleRegister(w http.ResponseWriter, r *http.Request) {
	authMu.Lock()
	defer authMu.Unlock()
	if authExists() {
		httpErr(w, http.StatusForbidden, "an account already exists")
		return
	}
	var req struct{ Email, Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !strings.Contains(req.Email, "@") {
		httpErr(w, 400, "enter a valid email")
		return
	}
	if len(req.Password) < 8 {
		httpErr(w, 400, "password must be at least 8 characters")
		return
	}
	auth = authConfig{Email: req.Email}
	setPassword(req.Password)
	if err := saveAuth(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	setSessionCookie(w, r)
	writeJSON(w, map[string]any{"ok": true})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if rateLimited(clientIP(r)) {
		httpErr(w, http.StatusTooManyRequests, "too many attempts, try again in 15 minutes")
		return
	}
	var req struct{ Email, Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	emailOK := auth.Email == "" || subtle.ConstantTimeCompare([]byte(req.Email), []byte(auth.Email)) == 1
	if !authExists() || !emailOK || !verifyPassword(req.Password) {
		httpErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if auth.Email == "" { // migrate pre-email accounts on first successful login
		auth.Email = req.Email
		saveAuth()
	}
	if auth.TOTPSecret != "" {
		writeJSON(w, map[string]any{"mfa": true, "token": newMFAToken()})
		return
	}
	setSessionCookie(w, r)
	writeJSON(w, map[string]any{"ok": true})
}

func handleLoginMFA(w http.ResponseWriter, r *http.Request) {
	if rateLimited(clientIP(r)) {
		httpErr(w, http.StatusTooManyRequests, "too many attempts, try again in 15 minutes")
		return
	}
	var req struct{ Token, Code string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if !takeMFAToken(req.Token) {
		httpErr(w, http.StatusUnauthorized, "login expired — start again")
		return
	}
	if !codeValid(auth.TOTPSecret, req.Code) {
		httpErr(w, http.StatusUnauthorized, "wrong code")
		return
	}
	burnMFAToken(req.Token)
	setSessionCookie(w, r)
	writeJSON(w, map[string]any{"ok": true})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("gantry_s"); err == nil {
		sessMu.Lock()
		delete(sessions, c.Value)
		sessMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "gantry_s", Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, map[string]any{"ok": true})
}

// handleMe is public: it tells the frontend whether to register, log in, or proceed.
func handleMe(w http.ResponseWriter, r *http.Request) {
	if !authExists() {
		writeJSON(w, map[string]any{"setup": true})
		return
	}
	if !sessionValid(r) {
		httpErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ip := serverIP()
	if mockMode {
		ip = "203.0.113.10"
	}
	writeJSON(w, map[string]any{"authed": true, "version": version, "mock": mockMode, "ip": ip})
}
