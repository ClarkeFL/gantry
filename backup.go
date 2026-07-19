package main

// Full server backup: one tar.gz holding the panel's state dir, the gantry
// cron files, and every app's definition (env + domains, plus meta.json's
// sources/cron/categories) — uploaded to the configured S3 bucket, oldest
// pruned beyond the retention count. `gantry restore <file>` rebuilds a
// server from it. Databases are covered separately by dokku's own dumps.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type appBackup struct {
	Name    string            `json:"name"`
	Env     map[string]string `json:"env"`
	Domains []string          `json:"domains"`
}

func backupLogPath() string { return filepath.Join(dataDir, "backuplog") }

func logBackup(line string) {
	f, err := os.OpenFile(backupLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), line)
}

func addToTar(tw *tar.Writer, name string, data []byte, mode int64) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: mode, ModTime: time.Now()}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func buildBackupArchive() ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	// panel state dir (skip the backup log itself)
	err := filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dataDir, path)
		if rel == "backuplog" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return addToTar(tw, "state/"+filepath.ToSlash(rel), b, 0o600)
	})
	if err != nil {
		return nil, err
	}

	// gantry cron files
	if entries, err := os.ReadDir(cronDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "gantry-") {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(cronDir, e.Name())); err == nil {
				addToTar(tw, "cron.d/"+e.Name(), b, 0o644)
			}
		}
	}

	// live app definitions from dokku
	apps := []appBackup{}
	if out, err := dokku("--quiet", "apps:list"); err == nil {
		for _, name := range strings.Split(out, "\n") {
			name = strings.TrimSpace(name)
			if !appRe.MatchString(name) {
				continue
			}
			a := appBackup{Name: name, Env: map[string]string{}}
			if envJSON, err := dokku("config:export", "--format", "json", name); err == nil {
				json.Unmarshal([]byte(envJSON), &a.Env)
			}
			if doms, err := dokku("domains:report", name, "--domains-app-vhosts"); err == nil {
				a.Domains = strings.Fields(doms)
			}
			apps = append(apps, a)
		}
	}
	b, _ := json.MarshalIndent(apps, "", "  ")
	if err := addToTar(tw, "apps.json", b, 0o600); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func backupKeep() int {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if settings.BackupKeep > 0 {
		return settings.BackupKeep
	}
	return 7
}

func runServerBackup(progress func(string)) error {
	progress("[backup] collecting panel state and app definitions…")
	archive, err := buildBackupArchive()
	if err != nil {
		logBackup("failed: " + err.Error())
		return err
	}
	key := "gantry/panel-" + time.Now().Format("20060102-150405") + ".tar.gz"
	settingsMu.Lock()
	bucket := settings.S3Bucket
	settingsMu.Unlock()
	progress(fmt.Sprintf("[backup] uploading %d KB to s3://%s/%s", len(archive)/1024, bucket, key))
	if err := s3Put(key, archive); err != nil {
		logBackup("failed: " + err.Error())
		go notifyWebhook("gantry: server backup failed — " + err.Error())
		return err
	}
	keep := backupKeep()
	if keys, err := s3List("gantry/panel-"); err == nil && len(keys) > keep {
		for _, old := range keys[:len(keys)-keep] {
			if err := s3Delete(old); err == nil {
				progress("[backup] pruned old backup " + old)
			}
		}
	}
	logBackup("ok " + key + fmt.Sprintf(" (%d KB, keep %d)", len(archive)/1024, keep))
	progress("[backup] done — " + key)
	return nil
}

func lastServerBackup() string {
	b, err := os.ReadFile(backupLogPath())
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// restoreBackup rebuilds panel state, cron files, and dokku apps from an archive.
func restoreBackup(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var apps []appBackup
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(hdr.Name)
		switch {
		case strings.Contains(name, ".."):
			continue
		case name == "apps.json":
			json.Unmarshal(b, &apps)
		case strings.HasPrefix(name, "state/"):
			dst := filepath.Join(dataDir, strings.TrimPrefix(name, "state/"))
			os.MkdirAll(filepath.Dir(dst), 0o755)
			if err := os.WriteFile(dst, b, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case strings.HasPrefix(name, "cron.d/"):
			os.MkdirAll(cronDir, 0o755)
			os.WriteFile(filepath.Join(cronDir, strings.TrimPrefix(name, "cron.d/")), b, 0o644)
		}
		fmt.Println("restored", name)
	}
	// recreate apps in dokku (idempotent: existing apps keep running)
	for _, a := range apps {
		if !appRe.MatchString(a.Name) {
			continue
		}
		if _, err := dokku("apps:create", a.Name); err == nil {
			fmt.Println("created app", a.Name)
		}
		if len(a.Env) > 0 {
			args := []string{"config:set", "--no-restart", a.Name}
			for k, v := range a.Env {
				args = append(args, k+"="+v)
			}
			dokku(args...)
		}
		for _, d := range a.Domains {
			dokku("domains:add", a.Name, d)
		}
	}
	fmt.Printf("\nrestore complete: %d apps defined. Restart the panel (systemctl restart gantry),\n", len(apps))
	fmt.Println("log in, and press Deploy on each app to rebuild it from its stored source.")
	fmt.Println("Databases: restore dumps from S3 with `dokku <type>:import <name> < dump`.")
	return nil
}
