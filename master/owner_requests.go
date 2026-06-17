package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Owner action requests: when an agent needs the OWNER to do something OUTSIDE the system — grant
// access to a portal, add an API key, authorize an app, approve in a console, etc. — it appends a
// brief entry (with a how-to guide + the URLs) to logs/owner-requests.json. The dashboard surfaces
// these in "Needs you" so the owner can act, then dismiss. This is the catch-all for any external need.

type OwnerRequest struct {
	ID    string   `json:"id"`    // stable id (agent sets, e.g. "azure-portal-access")
	Title string   `json:"title"` // one-line ask
	Guide string   `json:"guide"` // short how-to (steps)
	URLs  []string `json:"urls"`  // links the owner needs to open
	Who   string   `json:"who"`   // requesting agent/role
	TS    int64    `json:"ts"`    // unix sec
	Done  bool     `json:"done"`
}

var orMu sync.Mutex

func ownerReqPath(cfg *Config) string {
	return filepath.Join(cfg.resolve(cfg.LogDir), "owner-requests.json")
}

func loadOwnerRequests(cfg *Config) []OwnerRequest {
	orMu.Lock()
	defer orMu.Unlock()
	b, err := os.ReadFile(ownerReqPath(cfg))
	if err != nil {
		return nil
	}
	var rs []OwnerRequest
	_ = json.Unmarshal(b, &rs)
	return rs
}

// ownerRequests returns the OPEN (not-done) requests, newest first, for the dashboard.
func ownerRequests(cfg *Config) []OwnerRequest {
	all := loadOwnerRequests(cfg)
	out := make([]OwnerRequest, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		if !all[i].Done {
			out = append(out, all[i])
		}
	}
	return out
}

// resolveOwnerRequest marks a request done (the owner handled it).
func resolveOwnerRequest(cfg *Config, id string) {
	orMu.Lock()
	defer orMu.Unlock()
	b, err := os.ReadFile(ownerReqPath(cfg))
	if err != nil {
		return
	}
	var rs []OwnerRequest
	if json.Unmarshal(b, &rs) != nil {
		return
	}
	for i := range rs {
		if rs[i].ID == id || rs[i].Title == id {
			rs[i].Done = true
		}
	}
	nb, _ := json.MarshalIndent(rs, "", "  ")
	_ = osWriteBytes(ownerReqPath(cfg), nb)
}

// addOwnerRequest appends a request (used by the /api and could be used by a helper). Dedups by ID.
func addOwnerRequest(cfg *Config, req OwnerRequest) {
	orMu.Lock()
	defer orMu.Unlock()
	var rs []OwnerRequest
	if b, err := os.ReadFile(ownerReqPath(cfg)); err == nil {
		_ = json.Unmarshal(b, &rs)
	}
	if req.TS == 0 {
		req.TS = time.Now().Unix()
	}
	for i := range rs {
		if rs[i].ID != "" && rs[i].ID == req.ID {
			rs[i] = req // update existing
			nb, _ := json.MarshalIndent(rs, "", "  ")
			_ = osWriteBytes(ownerReqPath(cfg), nb)
			return
		}
	}
	rs = append(rs, req)
	nb, _ := json.MarshalIndent(rs, "", "  ")
	_ = osWriteBytes(ownerReqPath(cfg), nb)
}
