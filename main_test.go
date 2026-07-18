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
