package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Connection metadata + a live per-device "Try" check for the Devices table. Each device's `reach`
// (an IP, a /dev/cu.* serial path, "adb", or an iPhone marker) tells us HOW to connect to it; the
// check runs the matching probe (ping / TCP-22 / serial-present / adb / idevice) and reports back.

// deviceConn derives (method, address, howTo) from a device's reach + type. No secrets shown.
func deviceConn(d map[string]any) (method, address, howTo string) {
	reach, _ := d["reach"].(string)
	typ, _ := d["type"].(string)
	user, _ := d["user"].(string)
	reach = strings.TrimSpace(reach)
	// explicit method override set in the editor wins over auto-derivation
	if ov, _ := d["method"].(string); ov != "" && !strings.EqualFold(ov, "auto") {
		addr := reach
		if ip := ipv4Re.FindString(reach); ip != "" {
			addr = ip
		}
		if strings.EqualFold(ov, "SSH") && user != "" {
			addr = user + "@" + addr
		}
		return ov, addr, ov + " · " + reach
	}
	switch {
	case strings.HasPrefix(reach, "/dev/") || strings.Contains(strings.ToLower(reach), "serial") && strings.HasPrefix(reach, "/"):
		return "USB serial", reach, "Plug in over USB — appears as " + reach + " (flash/monitor via the serial port)"
	case strings.EqualFold(reach, "adb") || typ == "android":
		return "ADB", reach, "Android over USB cable or Wi-Fi — `adb devices` (or `adb connect <ip>:5555`)"
	case strings.Contains(strings.ToLower(reach), "iphone") || strings.Contains(strings.ToLower(reach), "idevice") || typ == "iphone" || typ == "ios":
		return "Cable / Wi-Fi", reach, "iPhone over USB cable, or Wi-Fi on the same network (pair once)"
	case ipv4Re.MatchString(reach):
		ip := ipv4Re.FindString(reach)
		// hubs / PCs / servers are reached by SSH; plain sensors by network ping
		if typ == "hub" || typ == "pc" || typ == "windows" || typ == "server" || typ == "pi" {
			addr := ip
			u := "<user>"
			if user != "" {
				addr = user + "@" + ip
				u = user
			}
			return "SSH", addr, "SSH over the LAN: `ssh " + u + "@" + ip + "`"
		}
		return "Network", ip, "On the LAN at " + ip + " (HTTP/MQTT/ping)"
	case reach == "":
		return "—", "", "No reach configured for this device"
	default:
		return "Other", reach, reach
	}
}

// tcpOpen reports whether a TCP port answers within a short timeout (used for SSH :22 checks).
func tcpOpen(host string, port int, t time.Duration) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), t)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// checkDevice runs the live connectivity probe appropriate to a device and returns a result map:
// {ok, method, address, detail}. It's quick (bounded timeouts) and safe (read-only probes).
func checkDevice(cfg *Config, name string) map[string]any {
	res := map[string]any{"name": name, "ok": false, "method": "", "address": "", "detail": "not found"}
	b, err := os.ReadFile(cfg.resolve(cfg.LogDir) + "/devices.json")
	if err != nil {
		return res
	}
	var ds []map[string]any
	if json.Unmarshal(b, &ds) != nil {
		return res
	}
	var dev map[string]any
	for _, d := range ds {
		if n, _ := d["name"].(string); n == name {
			dev = d
			break
		}
	}
	if dev == nil {
		return res
	}
	method, address, _ := deviceConn(dev)
	res["method"], res["address"] = method, address
	reach, _ := dev["reach"].(string)
	typ, _ := dev["type"].(string)

	switch method {
	case "USB serial":
		ms, _ := filepath.Glob(reach)
		if len(ms) > 0 || fileExists(reach) {
			res["ok"], res["detail"] = true, "serial port present ("+reach+")"
		} else {
			res["detail"] = "serial port not found — plug in the USB cable"
		}
	case "ADB":
		out := runc("adb devices 2>/dev/null", 5*time.Second)
		on := false
		for _, ln := range strings.Split(out, "\n") {
			ln = strings.TrimSpace(ln)
			if (strings.HasSuffix(ln, "\tdevice") || strings.HasSuffix(ln, " device")) && ln != "" {
				on = true
			}
		}
		res["ok"] = on
		if on {
			res["detail"] = "adb device attached"
		} else {
			res["detail"] = "no adb device — connect USB or `adb connect <ip>`"
		}
	case "Cable / Wi-Fi": // iPhone — libimobiledevice (older) OR `xcrun devicectl` (iOS 17+/tunnel) OR Wi-Fi
		cable := strings.TrimSpace(runc("idevice_id -l 2>/dev/null", 5*time.Second)) != ""
		// devicectl distinguishes "connected" (ACTIVE link) from "available (paired)" (a remembered
		// pairing — the phone is NOT actually attached). Only "connected" counts; "available (paired)"
		// does NOT (that was the false-positive: an unplugged-but-paired iPhone showed as connected).
		active := false // actively connected per devicectl
		host := ""      // its <name>.coredevice.local hostname — used as a liveness tiebreaker
		for _, ln := range strings.Split(runc("xcrun devicectl list devices 2>/dev/null", 9*time.Second), "\n") {
			l := strings.ToLower(ln)
			if !strings.Contains(l, "iphone") && !strings.Contains(l, "ipad") {
				continue
			}
			if strings.Contains(l, "connected") && !strings.Contains(l, "available") {
				active = true // a real, live connection
			}
			for _, f := range strings.Fields(ln) {
				if strings.HasSuffix(strings.ToLower(f), ".local") {
					host = f
				}
			}
		}
		// Liveness tiebreaker: a paired phone's coredevice host only RESOLVES + answers when the
		// device tunnel/Wi-Fi link is actually up; when it's unplugged the name doesn't resolve.
		hostUp := host != "" && pingable(host)
		ip := ipv4Re.FindString(reach)
		wifi := ip != "" && pingable(ip)
		res["ok"] = cable || active || hostUp || wifi
		switch {
		case cable:
			res["detail"] = "connected by USB cable"
		case active:
			res["detail"] = "actively connected (devicectl)"
		case hostUp:
			res["detail"] = "reachable at " + host
		case wifi:
			res["detail"] = "reachable over Wi-Fi at " + ip
		default:
			res["detail"] = "paired but NOT connected — plug in the cable or bring up the WDA tunnel"
		}
	case "SSH":
		ip := ipv4Re.FindString(address)
		if ip == "" {
			ip = address
		}
		// if a username + password are stored, do a REAL login test (proves true access, not just an
		// open port). Falls back to ping / port-22 if creds are absent or the login fails.
		user, _ := dev["user"].(string)
		pass, _ := dev["pass"].(string)
		if user != "" && pass != "" {
			if okv, det := sshLoginCheck(user, pass, ip); okv {
				res["ok"], res["detail"] = true, det
				return res
			}
		}
		ping := pingable(ip)
		ssh := tcpOpen(ip, 22, 2*time.Second)
		res["ok"] = ping || ssh
		switch {
		case ssh:
			res["detail"] = "SSH port 22 open at " + ip + (map[bool]string{true: " · ping ok", false: ""})[ping]
		case ping:
			res["detail"] = "host up at " + ip + " but SSH (:22) not answering"
		default:
			res["detail"] = "no response from " + ip + " (off / not on the LAN)"
		}
	case "Network":
		ip := address
		if pingable(ip) {
			res["ok"], res["detail"] = true, "responds to ping at "+ip
		} else {
			res["detail"] = "no ping response from " + ip
		}
	default:
		// best effort: if reach looks like an IP, ping it
		if ip := ipv4Re.FindString(reach); ip != "" {
			res["ok"] = pingable(ip)
			res["detail"] = map[bool]string{true: "reachable at " + ip, false: "no response from " + ip}[res["ok"].(bool)]
		} else {
			res["detail"] = "no live check for this connection type (" + typ + ")"
		}
	}
	return res
}

// ── background auto-check ───────────────────────────────────────────────────────────────────────
// Periodically run the deep per-device check (SSH :22, serial-present, adb, iPhone, ping) for EVERY
// device so the table's status stays fresh on its own — not just the quick 8s ping scan, and without
// the owner having to tap Try. Results are cached and overlaid onto the devices() payload.
var (
	devCheckMu    sync.Mutex
	devCheckCache = map[string]map[string]any{} // device name -> {ok, method, detail, ts}
)

func startDeviceCheckLoop(cfg *Config) {
	go func() {
		time.Sleep(8 * time.Second) // let the first quick scan populate first
		for {
			b, err := os.ReadFile(cfg.resolve(cfg.LogDir) + "/devices.json")
			if err == nil {
				var ds []map[string]any
				if json.Unmarshal(b, &ds) == nil {
					for _, d := range ds {
						name, _ := d["name"].(string)
						if name == "" || d["kind"] == "sim" {
							continue // sims are always "connected"; skip the network probe
						}
						res := checkDevice(cfg, name)
						res["ts"] = time.Now().Unix()
						devCheckMu.Lock()
						devCheckCache[name] = res
						devCheckMu.Unlock()
						time.Sleep(500 * time.Millisecond) // gentle: don't burst the network
					}
				}
			}
			time.Sleep(90 * time.Second) // re-check every ~90s
		}
	}()
}

// devCheckGet returns the cached deep-check result for a device (nil if none yet).
func devCheckGet(name string) map[string]any {
	devCheckMu.Lock()
	defer devCheckMu.Unlock()
	return devCheckCache[name]
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
