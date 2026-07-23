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

func TestProjectEnvApply(t *testing.T) {
	oldProj := map[string]string{"A": "1", "B": "2", "C": "3", "D": "4"}
	newProj := map[string]string{"A": "1", "B": "20", "D": "40", "E": "5"}
	appEnv := map[string]string{
		"A":   "1",   // inherited, unchanged -> nothing to do
		"B":   "2",   // inherited, project changed -> set B=20
		"C":   "3",   // inherited, project removed -> unset
		"D":   "999", // app override -> untouched despite project change
		"OWN": "x",   // app-only key -> untouched
	}
	set, unset := projectEnvApply(oldProj, newProj, appEnv)
	if len(set) != 2 || set["B"] != "20" || set["E"] != "5" {
		t.Errorf("set = %v, want B=20 E=5", set)
	}
	if len(unset) != 1 || unset[0] != "C" {
		t.Errorf("unset = %v, want [C]", unset)
	}
	// app missing an inherited key (e.g. joined late) gets it on next save
	set, _ = projectEnvApply(oldProj, newProj, map[string]string{})
	if set["A"] != "1" || set["B"] != "20" {
		t.Errorf("empty app env should receive all keys, got %v", set)
	}
}

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.11.0", "v0.12.0", false}, // never offer a downgrade
		{"v0.12.0", "v0.12.0", false},
		{"v0.12.1", "v0.12.0", true},
		{"v1.0.0", "v0.99.9", true},
		{"v0.9.1", "v0.12.0", false}, // numeric, not alphabetic, compare
		{"v0.12.0", "dev", true},     // dev builds always see releases
	}
	for _, c := range cases {
		if got := newerVersion(c.latest, c.current); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}
