package main

import (
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type appStat struct {
	App string `json:"app"`
	CPU string `json:"cpu"`
	Mem string `json:"mem"`
}

// One point per 5s sample; percents for bounded metrics, B/s for network.
type statPoint struct {
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
	Disk float64 `json:"disk"`
	Net  float64 `json:"net"`
}

const histCap = 120 // 10 minutes at 5s

var (
	statsMu   sync.Mutex
	statsApps []appStat
	statsHist []statPoint
	statsCur  struct {
		CPU                            float64
		MemUsed, MemTotal              uint64
		DiskUsed, DiskTotal            uint64
		Net                            float64
		Load                           string
		Cores                          int
	}
)

func startStatsSampler() {
	if mockMode {
		seedMockStats()
	}
	// docker stats takes ~2s to sample, far too slow to run per request, so a
	// second loop refreshes a cache every 10s and handleStats reads that.
	go func() {
		for {
			apps := containerStats()
			statsMu.Lock()
			statsApps = apps
			statsMu.Unlock()
			time.Sleep(10 * time.Second)
		}
	}()
	go func() {
		var lastIdle, lastTotal, lastRx, lastTx uint64
		lastIdle, lastTotal = readCPU()
		lastRx, lastTx = readNet()
		for range time.Tick(5 * time.Second) {
			if mockMode {
				appendMockPoint()
				continue
			}
			idle, total := readCPU()
			rx, tx := readNet()
			cpu := 0.0
			if total > lastTotal {
				cpu = 100 * (1 - float64(idle-lastIdle)/float64(total-lastTotal))
			}
			net := float64(rx-lastRx+tx-lastTx) / 5
			lastIdle, lastTotal, lastRx, lastTx = idle, total, rx, tx

			memUsed, memTotal := readMem()
			var st syscall.Statfs_t
			syscall.Statfs("/", &st)
			diskTotal := st.Blocks * uint64(st.Bsize)
			diskUsed := diskTotal - st.Bavail*uint64(st.Bsize)

			statsMu.Lock()
			statsCur.CPU, statsCur.Net = cpu, net
			statsCur.MemUsed, statsCur.MemTotal = memUsed, memTotal
			statsCur.DiskUsed, statsCur.DiskTotal = diskUsed, diskTotal
			statsCur.Load = readLoad()
			statsCur.Cores = runtime.NumCPU()
			pushPoint(statPoint{cpu, pct(memUsed, memTotal), pct(diskUsed, diskTotal), net})
			statsMu.Unlock()
		}
	}()
}

func pct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(used) / float64(total)
}

func pushPoint(p statPoint) { // callers hold statsMu
	statsHist = append(statsHist, p)
	if len(statsHist) > histCap {
		statsHist = statsHist[len(statsHist)-histCap:]
	}
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	statsMu.Lock()
	cur := statsCur
	hist := append([]statPoint{}, statsHist...)
	apps := append([]appStat{}, statsApps...)
	statsMu.Unlock()
	writeJSON(w, map[string]any{
		"cpu":  map[string]any{"pct": cur.CPU, "cores": cur.Cores, "load": cur.Load},
		"mem":  map[string]uint64{"used": cur.MemUsed, "total": cur.MemTotal},
		"disk": map[string]uint64{"used": cur.DiskUsed, "total": cur.DiskTotal},
		"net":  cur.Net,
		"hist": hist,
		"apps": apps,
	})
}

// --- /proc readers (linux; zeros elsewhere, which mock mode papers over) ---

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

func readLoad() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return ""
	}
	return strings.Join(f[:3], ", ")
}

// containerStats maps docker usage to dokku app names (<app>.web.1 → <app>).
func containerStats() []appStat {
	if mockMode {
		return []appStat{{"blog", "0.12%", "48MiB / 3.8GiB"}, {"api", "1.03%", "156MiB / 3.8GiB"}}
	}
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

// --- mock data: a plausible random walk so the UI has life during development ---

var mockWalk = statPoint{20, 22, 22.5, 60_000}

func seedMockStats() {
	statsMu.Lock()
	defer statsMu.Unlock()
	for i := 0; i < histCap; i++ {
		stepMockWalk()
		pushPoint(mockWalk)
	}
	syncMockCur()
}

func appendMockPoint() {
	statsMu.Lock()
	defer statsMu.Unlock()
	stepMockWalk()
	pushPoint(mockWalk)
	syncMockCur()
}

func stepMockWalk() {
	clamp := func(v, lo, hi float64) float64 {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	mockWalk.CPU = clamp(mockWalk.CPU+rand.Float64()*14-7, 2, 95)
	mockWalk.Mem = clamp(mockWalk.Mem+rand.Float64()*2-1, 15, 60)
	mockWalk.Disk = clamp(mockWalk.Disk+rand.Float64()*0.1-0.04, 20, 30)
	mockWalk.Net = clamp(mockWalk.Net+rand.Float64()*40_000-20_000, 0, 900_000)
}

func syncMockCur() { // callers hold statsMu
	total := uint64(3888) << 20
	disk := uint64(40) << 30
	statsCur.CPU, statsCur.Net = mockWalk.CPU, mockWalk.Net
	statsCur.MemTotal, statsCur.MemUsed = total, uint64(mockWalk.Mem/100*float64(total))
	statsCur.DiskTotal, statsCur.DiskUsed = disk, uint64(mockWalk.Disk/100*float64(disk))
	statsCur.Load = "0.42, 0.31, 0.18"
	statsCur.Cores = 2
}
