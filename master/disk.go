package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// swapUsedPct returns macOS swap utilization %. A near-full swap = memory death-spiral risk
// (the hang condition), which RAM-% alone misses when other apps (e.g. LM Studio) hold memory.
func swapUsedPct() int {
	out, _ := exec.Command("bash", "-lc", "sysctl -n vm.swapusage").Output()
	f := strings.Fields(string(out)) // "total = 11264.00M  used = 10046.81M  free = ..."
	var total, used float64
	for i, t := range f {
		if t == "total" && i+2 < len(f) {
			total, _ = strconv.ParseFloat(strings.TrimRight(f[i+2], "M"), 64)
		}
		if t == "used" && i+2 < len(f) {
			used, _ = strconv.ParseFloat(strings.TrimRight(f[i+2], "M"), 64)
		}
	}
	if total <= 0 {
		return 0
	}
	return int(used / total * 100)
}

// workerProcCount counts live fleet worker processes (headless claude/codex + sub-agents).
// A backstop against runaway spawning (the fanout fork-bomb that hung the Mac at 2325 procs).
func workerProcCount() int {
	out, _ := exec.Command("bash", "-lc", "pgrep -f 'claude -p|codex exec|codex-task|grep -r|pytest|ffmpeg' | wc -l").Output()
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// resourceOK is the launch gate: refuse to start new workers when the machine is under
// resource pressure (disk, memory, load, or too many worker processes) so it can NEVER hang.
// Lowering never kills running agents — it just stops launching until pressure drops.
func resourceOK(cfg *Config) bool {
	s := sysStatus(cfg)
	minFree := cfg.MinFreeGB
	if minFree <= 0 {
		minFree = 20 // sane default even if unset (the disk-hang happened with it =0/off)
	}
	if s.DiskFreeGB > 0 && s.DiskFreeGB < minFree {
		notifyOwnerThrottled(cfg, fmt.Sprintf("low disk: %dGB free (<%dGB) — pausing new workers", s.DiskFreeGB, minFree))
		return false
	}
	if s.MemUsedPct >= 92 { // real memory pressure (memory_pressure-based) → hold
		return false
	}
	// High swap ALLOCATION alone is normal on macOS (it keeps pages mapped to swap even when memory
	// is plentiful). Only treat it as a hang risk when REAL memory pressure (memory_pressure-based
	// MemUsedPct) is ALSO elevated — otherwise a long-uptime Mac with healthy free memory would
	// wrongly block the whole fleet (observed: swap 94% but 50% memory free → 0 workers launched).
	if swapUsedPct() >= 90 && s.MemUsedPct >= 75 {
		return false
	}
	if l, err := strconv.ParseFloat(s.Load, 64); err == nil {
		if l > float64(runtime.NumCPU())*2.0 { // load way above core count → hold
			return false
		}
	}
	if workerProcCount() > 60 { // runaway backstop
		return false
	}
	return true
}

// freeGB returns free disk space (GB) at path, or -1 on error.
func freeGB(path string) float64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return -1
	}
	return float64(st.Bavail) * float64(st.Bsize) / 1e9
}

// runCleanup reclaims disk regularly so the Mac never fills up again: prunes git worktree refs,
// removes CLEAN agent worktrees older than 3h (their commits live on pushed branches), deletes
// old logs, and — when disk is getting low — clears regenerable Xcode/Gradle build caches.
func runCleanup(cfg *Config) {
	proj := expandHome(cfg.resolve(cfg.ProjectDir))
	low := freeGB(proj) < float64(cfg.MinFreeGB)+10
	script := `set +e
PROJ="` + proj + `"
# remove CLEAN worktrees not touched in 3h (committed work is safe on branches)
if [ -d "$PROJ/.worktrees" ]; then
  for wt in "$PROJ"/.worktrees/*/; do
    [ -d "$wt" ] || continue
    if [ -z "$(git -C "$wt" status --porcelain 2>/dev/null)" ] && [ -z "$(find "$wt" -maxdepth 0 -mmin -180 2>/dev/null)" ]; then
      rm -rf "$wt"
    fi
  done
fi
# prune dangling worktree refs in every clone
for r in $(find "$PROJ" -maxdepth 5 -name .git -type d 2>/dev/null | sed 's:/.git$::'); do git -C "$r" worktree prune 2>/dev/null; done
# old logs / screenshots
find "$PROJ/Claud_worker_agent/logs" -type f -mtime +7 -delete 2>/dev/null
`
	if low { // getting low → drop regenerable build caches too
		script += "rm -rf ~/Library/Developer/Xcode/DerivedData/* 2>/dev/null\n"
		script += "rm -rf ~/.gradle/caches/build-cache-* 2>/dev/null\n"
	}
	_ = exec.Command("bash", "-lc", script).Run()
	logf(cfg, "cleanup", "ran disk cleanup (free now %.0fGB)", freeGB(proj))
}

// diskOK reports whether there's enough free disk to launch more workers.
// On low disk it pings the owner (throttled) to clean up or attach the external drive.
func diskOK(cfg *Config) bool {
	if cfg.MinFreeGB <= 0 {
		return true
	}
	f := freeGB(cfg.resolve(cfg.ProjectDir))
	if f < 0 {
		return true // unknown → fail open
	}
	if f < float64(cfg.MinFreeGB) {
		notifyOwnerThrottled(cfg, fmt.Sprintf("low disk: %.0fGB free (<%dGB) — paused new workers. Clean logs/screenshots, or attach the external drive and point logDir/artifacts to it.", f, cfg.MinFreeGB))
		return false
	}
	return true
}
