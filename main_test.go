package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTOTP(t *testing.T) {
	// RFC 6238 test vectors (SHA-1, last 6 digits)
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924"} {
		if got := totp(secret, time.Unix(ts, 0)); got != want {
			t.Errorf("totp at %d = %q, want %q", ts, got, want)
		}
	}
}

func TestCronFile(t *testing.T) {
	dataDir, cronDir = t.TempDir(), t.TempDir()
	jobs := []cronJob{{ID: "abc", Schedule: "0 3 * * *", Command: "echo 'hi' 100%"}}
	if err := writeCronFile("blog", jobs); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(cronDir, "gantry-blog"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"0 3 * * * root sh -c", "dokku --rm run blog", `'\''hi'\''`, `100\%`, "exit=$?"} {
		if !strings.Contains(s, want) {
			t.Errorf("cron file missing %q:\n%s", want, s)
		}
	}
	if err := writeCronFile("blog", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cronDir, "gantry-blog")); !os.IsNotExist(err) {
		t.Error("cron file should be removed when no jobs remain")
	}
	if !validSchedule("@daily") || !validSchedule("*/5 * * * *") || validSchedule("bogus") {
		t.Error("validSchedule misbehaving")
	}
}

func TestBackupRestoreRoundtrip(t *testing.T) {
	src := t.TempDir()
	dataDir, cronDir = src, filepath.Join(src, "cron.d")
	os.MkdirAll(filepath.Join(src, "cronlog"), 0o755)
	os.MkdirAll(cronDir, 0o755)
	os.WriteFile(filepath.Join(src, "meta.json"), []byte(`{"api":{"repo":"https://github.com/x/y"}}`), 0o644)
	os.WriteFile(filepath.Join(src, "cronlog", "api-1.log"), []byte("2026-01-01 exit=0\n"), 0o644)
	os.WriteFile(filepath.Join(cronDir, "gantry-api"), []byte("* * * * * root echo hi\n"), 0o644)
	os.WriteFile(filepath.Join(src, "backuplog"), []byte("should not be archived\n"), 0o644)

	archive, err := buildBackupArchive()
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "b.tar.gz")
	os.WriteFile(tmp, archive, 0o644)

	dst := t.TempDir()
	dataDir, cronDir = dst, filepath.Join(dst, "cron.d")
	if err := restoreBackup(tmp); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "meta.json"))
	if err != nil || !strings.Contains(string(b), "github.com/x/y") {
		t.Fatalf("meta.json not restored: %v %q", err, b)
	}
	if _, err := os.ReadFile(filepath.Join(dst, "cronlog", "api-1.log")); err != nil {
		t.Fatal("nested cronlog file not restored")
	}
	if _, err := os.ReadFile(filepath.Join(dst, "cron.d", "gantry-api")); err != nil {
		t.Fatal("cron file not restored")
	}
	if _, err := os.Stat(filepath.Join(dst, "backuplog")); err == nil {
		t.Fatal("backuplog should be excluded from archives")
	}
}
