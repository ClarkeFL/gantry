package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	go func() { cmd.Wait(); pw.Close() }()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fn(sc.Text())
	}
	return nil
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
			if i == 0 || len(f) == 0 || strings.HasPrefix(f[0], "=") {
				continue // header
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
)

func mockDokku(args []string) (string, error) {
	mockMu.Lock()
	defer mockMu.Unlock()
	cmd := args[0]
	if cmd == "--quiet" {
		cmd = args[1]
	}
	verb, app := cmd, ""
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
	case strings.HasPrefix(verb, "letsencrypt:"):
		return "-----> OK", nil
	default:
		return "", fmt.Errorf("mock: unhandled dokku %s %s", verb, app)
	}
}
