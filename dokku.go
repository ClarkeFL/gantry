package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var mockMode = os.Getenv("GANTRY_MOCK") == "1"

func dokku(args ...string) (string, error) {
	if mockMode {
		return mockDokku(args)
	}
	out, err := exec.Command("dokku", args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("dokku %s: %s", strings.Join(args, " "), s)
	}
	return s, nil
}

// streamCmd runs a command and feeds each output line (stdout+stderr) to fn
// until the command exits or ctx is cancelled.
func streamCmd(ctx context.Context, fn func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- cmd.Wait(); pw.Close() }()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fn(sc.Text())
	}
	return <-errCh
}

type service struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

var mockServices = []service{{"postgres", "main-db", "running"}, {"redis", "cache", "running"}}

func listServices() []service {
	if mockMode {
		mockMu.Lock()
		defer mockMu.Unlock()
		return append([]service{}, mockServices...)
	}
	out := []service{}
	for _, plugin := range []string{"postgres", "mysql", "mariadb", "redis", "mongo"} {
		txt, err := dokku(plugin + ":list")
		if err != nil {
			continue // plugin not installed
		}
		for i, line := range strings.Split(txt, "\n") {
			f := strings.Fields(line)
			if i == 0 || len(f) == 0 || strings.HasPrefix(f[0], "=") || strings.HasPrefix(f[0], "!") {
				continue // header or "! There are no ... services"
			}
			s := service{Type: plugin, Name: f[0], Status: "?"}
			if len(f) >= 3 {
				s.Status = f[2] // ponytail: NAME VERSION STATUS column order of dokku *:list
			}
			out = append(out, s)
		}
	}
	return out
}

// --- mock dokku for developing the panel off-server (GANTRY_MOCK=1) ---

var (
	mockMu  sync.Mutex
	mockEnv = map[string]map[string]string{
		"blog":    {"NODE_ENV": "production", "PORT": "5000"},
		"api":     {"DATABASE_URL": "postgres://main-db:5432/api", "SECRET_KEY": "shhh"},
		"landing": {},
	}
	mockRunning = map[string]bool{"blog": true, "api": true, "landing": false}
	mockSSL     = map[string]bool{"blog": true}
	mockDomains = map[string][]string{
		"blog":    {"blog.example.com"},
		"api":     {"api.example.com"},
		"landing": {"example.com", "www.example.com"},
	}
	mockLinks       = map[string][]string{"postgres/main-db": {"api"}} // "type/name" -> apps
	mockSchedules   = map[string]string{}                             // "type/name" -> cron
	mockMaintenance = map[string]bool{}                               // app -> maintenance on
	// which dokku service plugins are "installed" in mock (postgres+redis match seed data)
	mockPlugins = map[string]bool{
		"postgres": true,
		"redis":    true,
		"mysql":    false,
		"mariadb":  false,
		"mongo":    false,
	}
)

// Env key/value injected when linking a service, matching real dokku plugins.
func mockLinkEnvKey(svcType string) string {
	switch svcType {
	case "postgres", "mysql", "mariadb":
		return "DATABASE_URL"
	case "redis":
		return "REDIS_URL"
	case "mongo":
		return "MONGO_URL"
	default:
		return strings.ToUpper(svcType) + "_URL"
	}
}

func mockLinkEnvValue(svcType, svcName, app string) string {
	switch svcType {
	case "postgres":
		return fmt.Sprintf("postgres://postgres:password@%s:5432/%s", svcName, app)
	case "mysql", "mariadb":
		return fmt.Sprintf("mysql://mysql:password@%s:3306/%s", svcName, app)
	case "redis":
		return fmt.Sprintf("redis://%s:6379", svcName)
	case "mongo":
		return fmt.Sprintf("mongodb://%s:27017/%s", svcName, app)
	default:
		return fmt.Sprintf("%s://%s", svcType, svcName)
	}
}

// mock state persists across panel restarts so dev behaves like real dokku
// (which keeps SSL/running/domain state itself).
type mockState struct {
	Env         map[string]map[string]string `json:"env"`
	Running     map[string]bool              `json:"running"`
	SSL         map[string]bool              `json:"ssl"`
	Domains     map[string][]string          `json:"domains"`
	Services    []service                    `json:"services"`
	Links       map[string][]string          `json:"links"`
	Schedules   map[string]string            `json:"schedules"`
	Maintenance map[string]bool              `json:"maintenance"`
	Plugins     map[string]bool              `json:"plugins"`
}

func mockStatePath() string { return filepath.Join(dataDir, "mockstate.json") }

// saveMockState is called with mockMu held.
func saveMockState() {
	b, _ := json.Marshal(mockState{mockEnv, mockRunning, mockSSL, mockDomains, mockServices, mockLinks, mockSchedules, mockMaintenance, mockPlugins})
	os.WriteFile(mockStatePath(), b, 0o644)
}

func loadMockState() {
	b, err := os.ReadFile(mockStatePath())
	if err != nil {
		return
	}
	var s mockState
	if json.Unmarshal(b, &s) != nil || s.Env == nil {
		return
	}
	mockEnv, mockRunning, mockSSL, mockDomains, mockServices = s.Env, s.Running, s.SSL, s.Domains, s.Services
	if s.Links != nil {
		mockLinks = s.Links
	}
	if s.Schedules != nil {
		mockSchedules = s.Schedules
	}
	if s.Maintenance != nil {
		mockMaintenance = s.Maintenance
	}
	if s.Plugins != nil {
		// merge so new types default in if we add them later
		for k, v := range s.Plugins {
			mockPlugins[k] = v
		}
	}
}

func mockDokku(args []string) (string, error) {
	mockMu.Lock()
	defer mockMu.Unlock()
	defer saveMockState()
	i := 0
	for i < len(args)-1 && strings.HasPrefix(args[i], "--") {
		i++
	}
	verb, app := args[i], ""
	if len(args) > 1 {
		app = args[len(args)-1]
	}
	switch {
	case verb == "apps:list":
		names := make([]string, 0, len(mockEnv))
		for k := range mockEnv {
			names = append(names, k)
		}
		sort.Strings(names)
		return strings.Join(names, "\n"), nil
	case verb == "config:export":
		app = args[len(args)-1]
		b, _ := json.Marshal(mockEnv[app])
		return string(b), nil
	case verb == "config:set", verb == "config:unset":
		rest := []string{}
		for _, a := range args[1:] {
			if !strings.HasPrefix(a, "--") {
				rest = append(rest, a)
			}
		}
		app = rest[0]
		if mockEnv[app] == nil {
			mockEnv[app] = map[string]string{}
		}
		for _, kv := range rest[1:] {
			if verb == "config:set" {
				k, v, _ := strings.Cut(kv, "=")
				mockEnv[app][k] = v
			} else {
				delete(mockEnv[app], kv)
			}
		}
		return "-----> OK", nil
	case verb == "apps:destroy":
		delete(mockEnv, app)
		delete(mockRunning, app)
		delete(mockDomains, app)
		delete(mockSSL, app)
		delete(mockMaintenance, app)
		return "-----> Destroyed " + app, nil
	case verb == "maintenance:on", verb == "maintenance:off":
		mockMaintenance[app] = verb == "maintenance:on"
		return "-----> OK", nil
	case verb == "plugin:installed":
		name := args[len(args)-1]
		if mockPlugins[name] {
			return "true", nil
		}
		return "", fmt.Errorf("plugin not installed")
	case strings.HasSuffix(verb, ":links"):
		svcType := strings.Split(verb, ":")[0]
		if !mockPlugins[svcType] {
			return "", fmt.Errorf("plugin not installed")
		}
		return strings.Join(mockLinks[svcType+"/"+args[i+1]], "\n"), nil
	case strings.HasSuffix(verb, ":link"):
		// Mirrors real dokku <plugin>:link: record the link and inject the
		// connection URL into the app's config (DATABASE_URL, REDIS_URL, …).
		svcType := strings.Split(verb, ":")[0]
		svcName, appName := args[i+1], args[i+2]
		key := svcType + "/" + svcName
		// avoid duplicate link entries
		already := false
		for _, a := range mockLinks[key] {
			if a == appName {
				already = true
				break
			}
		}
		if !already {
			mockLinks[key] = append(mockLinks[key], appName)
		}
		if mockEnv[appName] == nil {
			mockEnv[appName] = map[string]string{}
		}
		mockEnv[appName][mockLinkEnvKey(svcType)] = mockLinkEnvValue(svcType, svcName, appName)
		return "-----> Linked", nil
	case strings.HasSuffix(verb, ":unlink"):
		svcType := strings.Split(verb, ":")[0]
		svcName, appName := args[i+1], args[i+2]
		key := svcType + "/" + svcName
		kept := []string{}
		for _, a := range mockLinks[key] {
			if a != appName {
				kept = append(kept, a)
			}
		}
		mockLinks[key] = kept
		if mockEnv[appName] != nil {
			delete(mockEnv[appName], mockLinkEnvKey(svcType))
		}
		return "-----> Unlinked", nil
	case strings.HasSuffix(verb, ":backup-schedule-cat"):
		key := strings.Split(verb, ":")[0] + "/" + args[i+1]
		if s := mockSchedules[key]; s != "" {
			return s + " dokku " + strings.Split(verb, ":")[0] + ":backup " + args[i+1], nil
		}
		return "", fmt.Errorf("no schedule")
	case strings.HasSuffix(verb, ":backup-schedule"):
		mockSchedules[strings.Split(verb, ":")[0]+"/"+args[i+1]] = args[i+2]
		return "-----> Scheduled", nil
	case strings.HasSuffix(verb, ":backup-unschedule"):
		delete(mockSchedules, strings.Split(verb, ":")[0]+"/"+args[i+1])
		return "-----> Unscheduled", nil
	case strings.HasSuffix(verb, ":backup-auth"):
		return "-----> OK", nil
	case strings.HasSuffix(verb, ":destroy"):
		kept := mockServices[:0]
		for _, s := range mockServices {
			if s.Name != app {
				kept = append(kept, s)
			}
		}
		mockServices = kept
		return "-----> Destroyed " + app, nil
	case verb == "apps:create":
		if mockEnv[args[1]] != nil {
			return "", fmt.Errorf("app %s already exists", args[1])
		}
		mockEnv[args[1]] = map[string]string{}
		mockRunning[args[1]] = false
		return "-----> Creating " + args[1] + "...", nil
	case verb == "registry:login":
		return "Login Succeeded", nil
	case verb == "ps:report":
		return fmt.Sprint(mockRunning[args[1]]), nil
	case strings.HasPrefix(verb, "ps:"):
		mockRunning[args[1]] = verb != "ps:stop"
		return "-----> " + verb + " " + args[1], nil
	case verb == "domains:report":
		return strings.Join(mockDomains[args[1]], " "), nil
	case verb == "cron:list":
		return "", nil
	case verb == "domains:add":
		mockDomains[args[1]] = append(mockDomains[args[1]], args[2])
		return "-----> Added " + args[2], nil
	case verb == "domains:remove":
		kept := []string{}
		for _, d := range mockDomains[args[1]] {
			if d != args[2] {
				kept = append(kept, d)
			}
		}
		mockDomains[args[1]] = kept
		return "-----> Removed " + args[2], nil
	case verb == "letsencrypt:active":
		if mockSSL[args[1]] {
			return "true", nil
		}
		return "false", fmt.Errorf("not active")
	case strings.HasPrefix(verb, "letsencrypt:"), strings.HasPrefix(verb, "builder"):
		return "-----> OK", nil
	default:
		return "", fmt.Errorf("mock: unhandled dokku %s %s", verb, app)
	}
}
