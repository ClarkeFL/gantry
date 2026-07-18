package main

import (
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type appStat struct {
	App string `json:"app"`
	CPU string `json:"cpu"`
	Mem string `json:"mem"`
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	if mockMode {
		writeJSON(w, map[string]any{
			"cpu":  23.4,
			"mem":  map[string]uint64{"used": 812 << 20, "total": 3888 << 20},
			"disk": map[string]uint64{"used": 9 << 30, "total": 40 << 30},
			"net":  map[string]float64{"rx": 128_000, "tx": 43_000},
			"apps": []appStat{{"blog", "0.12%", "48MiB / 3.8GiB"}, {"api", "1.03%", "156MiB / 3.8GiB"}},
		})
		return
	}
	idle1, total1 := readCPU()
	rx1, tx1 := readNet()
	time.Sleep(500 * time.Millisecond)
	idle2, total2 := readCPU()
	rx2, tx2 := readNet()
	cpu := 0.0
	if total2 > total1 {
		cpu = 100 * (1 - float64(idle2-idle1)/float64(total2-total1))
	}
	memUsed, memTotal := readMem()
	var st syscall.Statfs_t
	syscall.Statfs("/", &st)
	diskTotal := st.Blocks * uint64(st.Bsize)
	diskFree := st.Bavail * uint64(st.Bsize)
	writeJSON(w, map[string]any{
		"cpu":  cpu,
		"mem":  map[string]uint64{"used": memUsed, "total": memTotal},
		"disk": map[string]uint64{"used": diskTotal - diskFree, "total": diskTotal},
		"net":  map[string]float64{"rx": float64(rx2-rx1) * 2, "tx": float64(tx2-tx1) * 2}, // bytes/sec (500ms window)
		"apps": containerStats(),
	})
}

// readCPU returns cumulative idle and total jiffies from /proc/stat.
func readCPU() (idle, total uint64) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	f := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	return
}

func readMem() (used, total uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	var avail uint64
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		kb, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = kb << 10
		case "MemAvailable:":
			avail = kb << 10
		}
	}
	return total - avail, total
}

// readNet returns cumulative rx/tx bytes across all interfaces except lo.
func readNet() (rx, tx uint64) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n")[2:] {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx += r
		tx += t
	}
	return
}

// containerStats maps docker container usage to dokku app names (<app>.web.1 → <app>).
func containerStats() []appStat {
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}").Output()
	if err != nil {
		return []appStat{}
	}
	stats := []appStat{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		app, _, _ := strings.Cut(f[0], ".")
		stats = append(stats, appStat{app, f[1], f[2]})
	}
	return stats
}
