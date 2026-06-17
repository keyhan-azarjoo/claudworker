package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func contextTimeout(t time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), t)
}

var (
	devMu        sync.Mutex
	devConnected map[string]bool
	ipv4Re       = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
)

func runc(c string, t time.Duration) string {
	ctx, cancel := contextTimeout(t)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "bash", "-lc", c).Output()
	return string(out)
}

// startDeviceWatch re-detects connected devices every ~8s so the panel reflects reality live.
func startDeviceWatch(cfg *Config) {
	go func() {
		for {
			detectDevices(cfg)
			time.Sleep(8 * time.Second)
		}
	}()
}

var devScanBusy int32

// scanDevicesNow runs an IMMEDIATE device rescan (the dashboard "recheck" button) without waiting
// for the 8s loop. Guarded so overlapping requests collapse into one in-flight scan.
func scanDevicesNow(cfg *Config) {
	if !atomic.CompareAndSwapInt32(&devScanBusy, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&devScanBusy, 0)
	detectDevices(cfg)
}

// deviceScanning reports whether a manual rescan is currently running (for the button's spinner).
func deviceScanning() bool { return atomic.LoadInt32(&devScanBusy) == 1 }

// pingable reports whether an IP actually answers. Uses 2 packets + a real wait so a single
// dropped packet over Wi-Fi (the old -c1 -t1 bug) no longer reads as "disconnected".
func pingable(ip string) bool {
	out := runc("ping -c2 -W2000 "+ip+" 2>/dev/null", 5*time.Second)
	return strings.Contains(out, "bytes from") || strings.Contains(out, " 0% packet loss") || strings.Contains(out, " 50.0% packet loss")
}

func detectDevices(cfg *Config) {
	conn := map[string]bool{}
	for _, g := range []string{"/dev/cu.usbserial*", "/dev/cu.usbmodem*", "/dev/cu.SLAB*", "/dev/cu.wchusbserial*"} {
		ms, _ := filepath.Glob(g)
		for _, m := range ms {
			conn[m] = true
		}
	}
	for _, ln := range strings.Split(runc("adb devices 2>/dev/null", 4*time.Second), "\n") {
		if strings.HasSuffix(strings.TrimSpace(ln), "\tdevice") || strings.HasSuffix(strings.TrimSpace(ln), " device") {
			conn["adb"] = true
		}
	}
	if strings.TrimSpace(runc("idevice_id -l 2>/dev/null", 4*time.Second)) != "" ||
		strings.Contains(strings.ToLower(runc("system_profiler SPUSBDataType 2>/dev/null | grep -i iphone", 7*time.Second)), "iphone") {
		conn["iphone"] = true
	}
	// Build the IP set from the curated device list (so EVERY device's IP is checked, incl.
	// hubs) plus a few infra defaults — no more hardcoded-and-incomplete list.
	ips := map[string]bool{
		"192.168.1.100": true, // example LAN device; real IPs come from your devices.json below
	}
	if cfg != nil {
		if b, err := os.ReadFile(cfg.resolve(cfg.LogDir) + "/devices.json"); err == nil {
			for _, ip := range ipv4Re.FindAllString(string(b), -1) {
				ips[ip] = true
			}
		}
	}
	for ip := range ips {
		if pingable(ip) {
			conn[ip] = true
		}
	}
	devMu.Lock()
	prev := devConnected
	devConnected = conn
	devMu.Unlock()
	// AUTO-START: if a device newly CONNECTED (was absent/not-detected, now present), kick the
	// hardware-waiting tickets so the fleet picks them up — "plug it in and it starts working".
	if prev != nil {
		for k := range conn {
			if !prev[k] {
				go triggerHardwareWaiting(cfg, k)
				wakeDeviceTests() // a device just connected → wake hardware/e2e/visual/qa tests to use it
				logf(cfg, "devices", "device connected (%s) → woke the device-test agents (run when you connect hardware, e.g. your phone at night)", k)
				break // one trigger per scan is enough
			}
		}
	}
}

var hwTrigMu sync.Mutex
var hwTrigAt time.Time

// triggerHardwareWaiting activates the hardware-waiting tickets (removes their wait labels so the
// fleet works them). Debounced so a flapping device doesn't spam Jira.
func triggerHardwareWaiting(cfg *Config, dev string) {
	hwTrigMu.Lock()
	if time.Since(hwTrigAt) < 2*time.Minute {
		hwTrigMu.Unlock()
		return
	}
	hwTrigAt = time.Now()
	hwTrigMu.Unlock()
	n := activateHardwareWaiting(cfg)
	if n > 0 {
		logf(cfg, "devices", "device %s connected → auto-started %d hardware-waiting ticket(s)", dev, n)
	}
}

// anyHardwareConnected reports whether ANY physical test device (USB serial, Android adb, or iPhone)
// is currently attached — the gate for the hardware-test agent (pending otherwise).
func anyHardwareConnected() bool {
	devMu.Lock()
	defer devMu.Unlock()
	for k := range devConnected {
		if k == "iphone" {
			if iphoneTestableNow() { // the real iPhone is testable ONLY 02:00–08:00 (owner connects it overnight)
				return true
			}
			continue
		}
		if strings.HasPrefix(k, "/dev/") || k == "adb" {
			return true
		}
	}
	return false
}

// deviceConnected matches a device's reach string against the latest detection.
func deviceConnected(reach string) bool {
	devMu.Lock()
	c := devConnected
	devMu.Unlock()
	if c == nil {
		return true // not scanned yet → assume present
	}
	r := strings.ToLower(reach)
	if strings.HasPrefix(reach, "/dev/cu") {
		return c[reach]
	}
	if strings.Contains(r, "adb") {
		return c["adb"]
	}
	if strings.Contains(r, "wda") || strings.Contains(reach, "00008") {
		return c["iphone"]
	}
	// Any IPv4 in the reach string that answered ping counts as connected.
	for _, ip := range ipv4Re.FindAllString(reach, -1) {
		if c[ip] {
			return true
		}
	}
	for _, tok := range strings.FieldsFunc(reach, func(r rune) bool { return r == ' ' || r == '/' }) {
		if c[strings.TrimSpace(tok)] {
			return true
		}
	}
	return false
}

// deviceClaims parses the shared Hardware Coordination board and returns, per device IP/id token,
// who is actively using it (rows NOT marked FREE/RELEASED). Lets the dashboard show "in use by …".
func deviceClaims(cfg *Config) map[string]string {
	out := map[string]string{}
	if cfg == nil {
		return out
	}
	b, err := os.ReadFile(cfg.resolve("../memory/HARDWARE_COORDINATION.md"))
	if err != nil {
		b, err = os.ReadFile("memory/HARDWARE_COORDINATION.md")
		if err != nil {
			return out
		}
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(ln), "|") {
			continue
		}
		low := strings.ToLower(ln)
		busy := strings.Contains(ln, "⛔") || strings.Contains(low, "do not touch") || strings.Contains(low, "avoid")
		// active claim = has a claimant and is NOT released/free
		if !busy {
			continue
		}
		cells := strings.Split(ln, "|")
		who := ""
		if len(cells) > 3 {
			who = strings.TrimSpace(cells[3])
		}
		if who == "" {
			who = "another agent"
		}
		// key off any IP or backtick id on the row
		for _, ip := range ipv4Re.FindAllString(ln, -1) {
			out[ip] = who
		}
		if m := regexp.MustCompile("`([^`]+)`").FindStringSubmatch(ln); m != nil {
			out[m[1]] = who
		}
	}
	return out
}
