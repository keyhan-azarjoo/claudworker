package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Device CRUD for the dashboard: the owner can add devices, edit name/type/IP-or-serial/username/
// password/note, or delete them. Auto-detected devices (USB serial / adb / iPhone) are added by the
// detector; manual ones (a Windows box, a hub by IP, …) are added here. Stored in logs/devices.json
// (gitignored, mode 600 — it can hold SSH passwords).

var devFileMu sync.Mutex

func loadDevicesFile(cfg *Config) []map[string]any {
	devFileMu.Lock()
	defer devFileMu.Unlock()
	b, err := os.ReadFile(cfg.resolve(cfg.LogDir) + "/devices.json")
	if err != nil {
		return []map[string]any{}
	}
	var ds []map[string]any
	if json.Unmarshal(b, &ds) != nil {
		return []map[string]any{}
	}
	return ds
}

func writeDevicesFile(cfg *Config, ds []map[string]any) error {
	devFileMu.Lock()
	defer devFileMu.Unlock()
	b, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return err
	}
	path := cfg.resolve(cfg.LogDir) + "/devices.json"
	if err := osWriteBytes(path, b); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600) // may contain device SSH passwords — keep private
	return nil
}

// saveDevice upserts a device. `oldName` identifies an existing row (for rename); empty = add new.
// An empty password on an EXISTING device keeps the stored one (so editing other fields is safe).
func saveDevice(cfg *Config, f map[string]string) {
	ds := loadDevicesFile(cfg)
	name := strings.TrimSpace(f["name"])
	if name == "" {
		return
	}
	old := strings.TrimSpace(f["oldName"])
	if old == "" {
		old = name
	}
	set := func(d map[string]any) {
		d["name"] = name
		d["type"] = strings.TrimSpace(f["type"])
		d["reach"] = strings.TrimSpace(f["reach"])
		d["user"] = strings.TrimSpace(f["user"])
		d["note"] = strings.TrimSpace(f["note"])
		if m := strings.TrimSpace(f["method"]); m != "" {
			d["method"] = m // explicit connect-method override ("" = auto-derive from reach/type)
		} else {
			delete(d, "method")
		}
		if v, ok := f["deployedVer"]; ok {
			d["deployedVer"] = strings.TrimSpace(v) // firmware/app version deployed to this device
		}
		if v, ok := f["deployedAt"]; ok {
			d["deployedAt"] = strings.TrimSpace(v) // when it was last deployed (YYYY-MM-DD)
		}
		d["account"] = strings.TrimSpace(f["account"]) // which account this device belongs to
		if m := strings.TrimSpace(f["mode"]); m != "" {
			d["mode"] = m // "online" (internet) | "offline" (local network / simulator only)
		}
		if _, ok := d["kind"]; !ok {
			d["kind"] = "real"
		}
		if p, has := f["pass"]; has && p != "" {
			d["pass"] = p // only overwrite when a new password is provided
		}
		d["manual"] = true
	}
	for _, d := range ds {
		if n, _ := d["name"].(string); n == old {
			set(d)
			_ = writeDevicesFile(cfg, ds)
			return
		}
	}
	// new device
	nd := map[string]any{"kind": "real", "inUse": false}
	set(nd)
	ds = append(ds, nd)
	_ = writeDevicesFile(cfg, ds)
}

// setDeviceAttr updates just a device's account / mode (online|offline) — the lightweight path the
// AI test agents use to flip a device without re-sending the whole edit form. Returns false if no device
// matched. Only the keys present in `attrs` are changed.
func setDeviceAttr(cfg *Config, name string, attrs map[string]string) bool {
	ds := loadDevicesFile(cfg)
	hit := false
	for _, d := range ds {
		if n, _ := d["name"].(string); n != name {
			continue
		}
		hit = true
		if v, ok := attrs["account"]; ok {
			d["account"] = strings.TrimSpace(v)
		}
		if v, ok := attrs["mode"]; ok {
			m := strings.ToLower(strings.TrimSpace(v))
			if m == "online" || m == "offline" {
				d["mode"] = m
			}
		}
	}
	if hit {
		_ = writeDevicesFile(cfg, ds)
	}
	return hit
}

func deleteDevice(cfg *Config, name string) {
	ds := loadDevicesFile(cfg)
	out := ds[:0]
	for _, d := range ds {
		if n, _ := d["name"].(string); n != name {
			out = append(out, d)
		}
	}
	_ = writeDevicesFile(cfg, out)
}

// devicePassword returns the stored password for a device (for the reveal button / login check).
func devicePassword(cfg *Config, name string) string {
	for _, d := range loadDevicesFile(cfg) {
		if n, _ := d["name"].(string); n == name {
			p, _ := d["pass"].(string)
			return p
		}
	}
	return ""
}

// sshLoginCheck attempts a REAL non-interactive SSH login (password via sshpass, env-passed so the
// password never appears in the process args/shell). Returns ok + a short detail.
func sshLoginCheck(user, pass, ip string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sshpass", "-e", "ssh",
		"-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=8",
		"-o", "PreferredAuthentications=password", "-o", "PubkeyAuthentication=no",
		"-o", "NumberOfPasswordPrompts=1", user+"@"+ip, "echo CW_LOGIN_OK")
	cmd.Env = append(os.Environ(), "SSHPASS="+pass)
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "CW_LOGIN_OK") {
		return true, "logged in as " + user + "@" + ip
	}
	return false, ""
}
