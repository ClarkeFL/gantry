package main

import (
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type appStat struct {
	App        string `json:"app"`
	CPU        string `json:"cpu"`
	Mem        string `json:"mem"`
	MemBytes   int64  `json:"memBytes"`
	Net        string `json:"net"` // rx+tx rate since the previous sample
	Containers int    `json:"containers"`
	IsApp      bool   `json:"isApp"` // links to the app page in the UI
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
		CPU                 float64
		MemUsed, MemTotal   uint64
		DiskUsed, DiskTotal uint64
		Net                 float64
		Load                string
		Cores               int
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

// containerLabel groups a docker container name into a display row:
// "<app>.web.1" → the app, "dokku.postgres.main-db" → "main-db (postgres)",
// anything else (dokku internals, stray containers) keeps its own name.
func containerLabel(name string) (label string, isApp bool) {
	if rest, ok := strings.CutPrefix(name, "dokku."); ok {
		if typ, svc, ok := strings.Cut(rest, "."); ok {
			return svc + " (" + typ + ")", false
		}
		return name, false
	}
	app, _, _ := strings.Cut(name, ".")
	metaMu.Lock()
	_, known := meta[app]
	metaMu.Unlock()
	return app, known
}

func parseSize(s string) int64 {
	units := []struct {
		suffix string
		mult   float64
	}{
		{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1},
	}
	s = strings.TrimSpace(s)
	for _, u := range units {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
			if err != nil {
				return 0
			}
			return int64(v * u.mult)
		}
	}
	return 0
}

func fmtBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + "GiB"
	case b >= 1<<20:
		return strconv.FormatInt(b/(1<<20), 10) + "MiB"
	case b >= 1<<10:
		return strconv.FormatInt(b/(1<<10), 10) + "KiB"
	default:
		return strconv.FormatInt(b, 10) + "B"
	}
}

// container network totals from the previous sample, for rate computation.
// Only the containerStats goroutine touches these.
var (
	prevCtrNet   = map[string]int64{}
	prevCtrNetAt time.Time
)

// containerStats aggregates docker usage for EVERY container on the server,
// summed per app / database service, largest memory first.
func containerStats() []appStat {
	if mockMode {
		return []appStat{
			{"api", "1.03%", "156MiB", 156 << 20, "14KiB/s", 2, true},
			{"main-db (postgres)", "0.40%", "89MiB", 89 << 20, "3KiB/s", 1, false},
			{"blog", "0.12%", "48MiB", 48 << 20, "1KiB/s", 1, true},
			{"cache (redis)", "0.08%", "12MiB", 12 << 20, "0B/s", 1, false},
		}
	}
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}").Output()
	if err != nil {
		return []appStat{}
	}
	type agg struct {
		cpu   float64
		mem   int64
		net   int64
		n     int
		isApp bool
	}
	rows := map[string]*agg{}
	order := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			continue
		}
		label, isApp := containerLabel(f[0])
		a := rows[label]
		if a == nil {
			a = &agg{isApp: isApp}
			rows[label] = a
			order = append(order, label)
		}
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(f[1], "%"), 64)
		a.cpu += cpu
		used, _, _ := strings.Cut(f[2], "/")
		a.mem += parseSize(used)
		rx, tx, _ := strings.Cut(f[3], "/")
		a.net += parseSize(rx) + parseSize(tx)
		a.n++
	}
	elapsed := time.Since(prevCtrNetAt).Seconds()
	nextNet := make(map[string]int64, len(rows))
	stats := make([]appStat, 0, len(order))
	for _, label := range order {
		a := rows[label]
		rate := ""
		if prev, ok := prevCtrNet[label]; ok && elapsed > 0 && a.net >= prev {
			rate = fmtBytes(int64(float64(a.net-prev)/elapsed)) + "/s"
		}
		nextNet[label] = a.net
		stats = append(stats, appStat{
			label, strconv.FormatFloat(a.cpu, 'f', 2, 64) + "%", fmtBytes(a.mem), a.mem, rate, a.n, a.isApp,
		})
	}
	prevCtrNet, prevCtrNetAt = nextNet, time.Now()
	sort.Slice(stats, func(i, j int) bool { return stats[i].MemBytes > stats[j].MemBytes })
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
