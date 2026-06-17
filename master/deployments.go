package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Deployments: per-service version + last-change date, read from each repo (version file + git).
// "Last commit on the deploy branch" is the best local proxy for "last deployed". Live services also
// get a health (up/down) from the same checks as the Servers page.

type DeployInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Date    string `json:"date"`   // last commit date (YYYY-MM-DD)
	Branch  string `json:"branch"`
	Commit  string `json:"commit"` // short hash
	Kind    string `json:"kind"`   // app / website / backend / service
	URL     string `json:"url"`    // live URL if any (for health)
}

type deploySvc struct{ name, path, kind, url string }

var deployServices = []deploySvc{
	{"📱 App (iOS/Android)", "User-Interface/Application/Flutter-IoT-MobileApp/claudworker", "app", ""},
	{"🏠 Hub", "User-Interface/Application/Flutter-Hub/claudworker-hub", "app", ""},
	{"🌐 Website", "User-Interface/WebSites/NextJs-Myotgo-Website", "website", "https://example.com"},
	{"🌐 Support site", "User-Interface/WebSites/NextJs-Myotgo-Support-Website", "website", "https://support.example.com"},
	{"⚙️ Backend API", "Backend/DotNet-WebApi-Backend/DotNet-IoT-MainWebApi", "backend", "https://api.example.com"},
	{"🔗 Matter Bridge", "Backend/Claudworker-Matter-Bridge", "service", ""},
	{"📣 Marketing", "Multimedia/Claudworker-Marketing-Agent", "service", "https://marketing.example.com"},
}

var (
	depCache []DeployInfo
	depAt    time.Time
	depMu    sync.Mutex
	depBusy  int32
	verRe    = regexp.MustCompile(`(?m)^version:\s*([^\s#]+)`)
	csprojRe = regexp.MustCompile(`<Version>([^<]+)</Version>`)
)

func deployments(cfg *Config) []DeployInfo {
	depMu.Lock()
	c, age := depCache, time.Since(depAt)
	depMu.Unlock()
	if c != nil && age < 5*time.Minute {
		return c
	}
	go refreshDeployments(cfg)
	return c
}

func refreshDeployments(cfg *Config) {
	if !atomic.CompareAndSwapInt32(&depBusy, 0, 1) {
		return // a refresh is already running — never pile up (this was the SSE-stall bug)
	}
	defer atomic.StoreInt32(&depBusy, 0)
	root := expandHome(cfg.resolve(cfg.ProjectDir))
	out := make([]DeployInfo, 0, len(deployServices))
	for _, s := range deployServices {
		dir := filepath.Join(root, s.path)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		di := DeployInfo{Name: s.name, Kind: s.kind, URL: s.url, Version: serviceVersion(dir)}
		di.Date = strings.TrimSpace(runc("git -C '"+dir+"' log -1 --format=%cd --date=short 2>/dev/null", 5*time.Second))
		di.Branch = strings.TrimSpace(runc("git -C '"+dir+"' rev-parse --abbrev-ref HEAD 2>/dev/null", 4*time.Second))
		di.Commit = strings.TrimSpace(runc("git -C '"+dir+"' rev-parse --short HEAD 2>/dev/null", 4*time.Second))
		out = append(out, di)
	}
	depMu.Lock()
	depCache, depAt = out, time.Now()
	depMu.Unlock()
}

// serviceVersion reads the version from whatever file the repo uses (pubspec / package.json / csproj).
func serviceVersion(dir string) string {
	if b, err := os.ReadFile(filepath.Join(dir, "pubspec.yaml")); err == nil {
		if m := verRe.FindSubmatch(b); m != nil {
			return string(m[1])
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var p struct{ Version string `json:"version"` }
		if json.Unmarshal(b, &p) == nil && p.Version != "" {
			return p.Version
		}
	}
	if cs, _ := filepath.Glob(filepath.Join(dir, "*.csproj")); len(cs) > 0 {
		if b, err := os.ReadFile(cs[0]); err == nil {
			if m := csprojRe.FindSubmatch(b); m != nil {
				return strings.TrimSpace(string(m[1]))
			}
		}
	}
	// python / other: a VERSION file if present
	if b, err := os.ReadFile(filepath.Join(dir, "VERSION")); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}
