package main

import (
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type SysStatus struct {
	DiskFreeGB  int    `json:"diskFreeGB"`
	DiskTotalGB int    `json:"diskTotalGB"`
	MemUsedPct  int    `json:"memUsedPct"`
	Load        string `json:"load"`
}

var (
	sysCache SysStatus
	sysAt    time.Time
	sysMu    sync.Mutex
)

func sh(c string) string {
	out, _ := exec.Command("bash", "-lc", c).Output()
	return strings.TrimSpace(string(out))
}

// sysStatus reports this Mac's disk/mem/load (cached 10s) for the dashboard's "this machine" panel.
func sysStatus(cfg *Config) SysStatus {
	sysMu.Lock()
	if time.Since(sysAt) < 10*time.Second && sysAt.Unix() > 0 {
		s := sysCache
		sysMu.Unlock()
		return s
	}
	sysMu.Unlock()
	s := SysStatus{}
	var st syscall.Statfs_t
	if syscall.Statfs(cfg.resolve(cfg.ProjectDir), &st) == nil {
		s.DiskFreeGB = int(float64(st.Bavail) * float64(st.Bsize) / 1e9)
		s.DiskTotalGB = int(float64(st.Blocks) * float64(st.Bsize) / 1e9)
	}
	// memory: use macOS's authoritative pressure metric (free %). The old vm_stat sum over-counted
	// "used" (cache/compressor) → showed ~96% when the machine was really ~71%, which made the
	// resource guard block everything. grep -m1 makes memory_pressure exit immediately.
	mp := sh("memory_pressure 2>/dev/null | grep -m1 'free percentage' | awk '{print $NF}' | tr -d '%'")
	if fp, e := strconv.ParseFloat(strings.TrimSpace(mp), 64); e == nil && fp > 0 {
		s.MemUsedPct = int(100 - fp)
	} else if total, _ := strconv.ParseFloat(sh("sysctl -n hw.memsize"), 64); total > 0 {
		// fallback to vm_stat if memory_pressure is unavailable
		freePages := 0.0
		for _, ln := range strings.Split(sh("vm_stat"), "\n") {
			if strings.HasPrefix(ln, "Pages free:") || strings.HasPrefix(ln, "Pages inactive:") || strings.HasPrefix(ln, "Pages speculative:") {
				f := strings.TrimRight(strings.TrimSpace(strings.SplitN(ln, ":", 2)[1]), ".")
				if n, e := strconv.ParseFloat(f, 64); e == nil {
					freePages += n
				}
			}
		}
		s.MemUsedPct = int((total - freePages*4096.0) / total * 100)
	}
	la := sh("sysctl -n vm.loadavg") // "{ 2.50 2.10 1.90 }"
	la = strings.NewReplacer("{", "", "}", "").Replace(la)
	if f := strings.Fields(la); len(f) >= 1 {
		s.Load = f[0]
	}
	sysMu.Lock()
	sysCache, sysAt = s, time.Now()
	sysMu.Unlock()
	return s
}

type ServerStatus struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Up   bool   `json:"up"`
	MS   int64  `json:"ms"`
	Code int    `json:"code"`
}

var (
	srvCache    []ServerStatus
	srvAt       time.Time
	srvMu       sync.Mutex
	srvRefresh  int32 // 1 = a refresh is already in flight
)

// serverHealth returns the cached server statuses INSTANTLY (never blocks the SSE on network).
// If the cache is stale it kicks off an async refresh and returns the previous values
// (stale-while-revalidate). The slow HTTP probes run WITHOUT holding the lock.
func serverHealth(cfg *Config) []ServerStatus {
	srvMu.Lock()
	c, age := srvCache, time.Since(srvAt)
	srvMu.Unlock()
	if c != nil && age < 30*time.Second {
		return c
	}
	// stale OR cold → refresh in the background and return whatever we have now (never block).
	go refreshServerHealth(cfg)
	return c
}

func refreshServerHealth(cfg *Config) []ServerStatus {
	if !atomic.CompareAndSwapInt32(&srvRefresh, 0, 1) {
		srvMu.Lock()
		c := srvCache
		srvMu.Unlock()
		return c // another refresh already running
	}
	defer atomic.StoreInt32(&srvRefresh, 0)
	targets := cfg.Monitor
	if len(targets) == 0 {
		targets = []MonitorTarget{
			{"backend (prod)", "https://api.example.com"},
			{"backend (staging)", "https://staging.api.example.com"},
			{"MQTT broker", "https://mqtt.example.com"},
			{"marketing", "https://marketing.example.com"},
			{"VPS", "http://your-server.example.com"},
		}
	}
	var wg sync.WaitGroup
	res := make([]ServerStatus, len(targets))
	for i, t := range targets {
		i, t := i, t
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			// Do NOT follow redirects: a 3xx means the server is UP. (Following the VPS :80 → 308 →
			// https://<ip>/ hit a cert-for-api.example.com mismatch and wrongly read as DOWN.)
			cli := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			req, _ := http.NewRequest("GET", t.URL, nil)
			resp, err := cli.Do(req)
			st := ServerStatus{Name: t.Name, URL: t.URL, MS: time.Since(start).Milliseconds()}
			if err == nil {
				st.Up = resp.StatusCode < 500
				st.Code = resp.StatusCode
				resp.Body.Close()
			}
			res[i] = st
		}()
	}
	wg.Wait()
	srvMu.Lock()
	srvCache, srvAt = res, time.Now()
	srvMu.Unlock()
	return res
}
