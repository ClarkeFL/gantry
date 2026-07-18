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
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
	TOTPSecret string `json:"totp_secret"`
}

var auth authConfig

func authPath() string { return filepath.Join(dataDir, "auth.json") }

func loadAuth() error {
	b, err := os.ReadFile(authPath())
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &auth)
}

func hashPassword(pw string, salt []byte) string {
	return base64.RawStdEncoding.EncodeToString(argon2.IDKey([]byte(pw), salt, 1, 64*1024, 4, 32))
}

func verifyPassword(pw string) bool {
	salt, _ := base64.RawStdEncoding.DecodeString(auth.Salt)
	return subtle.ConstantTimeCompare([]byte(hashPassword(pw, salt)), []byte(auth.Hash)) == 1
}

func saveAuth() error {
	b, _ := json.MarshalIndent(auth, "", "  ")
	return os.WriteFile(authPath(), b, 0o600)
}

func initAuth() {
	fmt.Print("New admin password (min 8 chars): ")
	var pw string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			fmt.Println("\nerror:", err)
			os.Exit(1)
		}
		pw = string(b)
		fmt.Println()
	} else {
		sc := bufio.NewScanner(os.Stdin)
		sc.Scan()
		pw = strings.TrimSpace(sc.Text())
	}
	if len(pw) < 8 {
		fmt.Println("password must be at least 8 characters")
		os.Exit(1)
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	secret := make([]byte, 20)
	rand.Read(secret)
	auth = authConfig{
		Salt:       base64.RawStdEncoding.EncodeToString(salt),
		Hash:       hashPassword(pw, salt),
		TOTPSecret: base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret),
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(auth, "", "  ")
	if err := os.WriteFile(authPath(), b, 0o600); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	fmt.Println("\nAdd gantry to your authenticator app (Google Authenticator, 1Password, ...):")
	fmt.Printf("\n  otpauth://totp/gantry?secret=%s&issuer=gantry\n", auth.TOTPSecret)
	fmt.Println("\nOr enter the secret manually:", auth.TOTPSecret)
	fmt.Println("\nDone. Start the panel with: gantry")
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

func totpValid(code string) bool {
	now := time.Now()
	ok := false
	for _, dt := range []time.Duration{0, -30 * time.Second, 30 * time.Second} {
		if subtle.ConstantTimeCompare([]byte(code), []byte(totp(auth.TOTPSecret, now.Add(dt)))) == 1 {
			ok = true
		}
	}
	return ok
}

// --- sessions (in-memory; a restart logs everyone out, which is fine) ---

const sessionTTL = 7 * 24 * time.Hour

var (
	sessMu   sync.Mutex
	sessions = map[string]time.Time{} // token -> expiry
)

func newSession() string {
	b := make([]byte, 32)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	sessMu.Lock()
	sessions[tok] = time.Now().Add(sessionTTL)
	sessMu.Unlock()
	return tok
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

// --- handlers ---

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if rateLimited(clientIP(r)) {
		httpErr(w, http.StatusTooManyRequests, "too many attempts, try again in 15 minutes")
		return
	}
	var req struct{ Password, Code string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if !verifyPassword(req.Password) || !totpValid(req.Code) {
		httpErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "gantry_s", Value: newSession(), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(sessionTTL.Seconds()),
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
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

func handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"authed": true, "version": version, "mock": mockMode})
}
