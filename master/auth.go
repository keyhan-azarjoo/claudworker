package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Users are stored hashed (sha256 + per-user salt) in users.json (gitignored), NOT in plaintext config.
// First run seeds admin/admin; the owner changes it and can add more users from the dashboard.
type userRec struct {
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}
type userStore struct {
	Users map[string]userRec `json:"users"`
}

var usersMu sync.Mutex

func usersPath(cfg *Config) string { return filepath.Join(cfg.root, "users.json") }

func randSalt() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func hashPass(salt, pass string) string {
	h := sha256.Sum256([]byte(salt + ":" + pass))
	return hex.EncodeToString(h[:])
}

func loadUsers(cfg *Config) *userStore {
	usersMu.Lock()
	defer usersMu.Unlock()
	st := &userStore{Users: map[string]userRec{}}
	b, err := os.ReadFile(usersPath(cfg))
	if err == nil {
		_ = json.Unmarshal(b, st)
	}
	if len(st.Users) == 0 { // first run → default admin/admin
		s := randSalt()
		st.Users["admin"] = userRec{Salt: s, Hash: hashPass(s, "admin")}
		b, _ := json.MarshalIndent(st, "", "  ")
		_ = os.WriteFile(usersPath(cfg), b, 0o600)
	}
	return st
}

func saveUsers(cfg *Config, st *userStore) {
	usersMu.Lock()
	defer usersMu.Unlock()
	b, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(usersPath(cfg), b, 0o600)
}

func checkUser(cfg *Config, name, pass string) bool {
	st := loadUsers(cfg)
	u, ok := st.Users[name]
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(u.Hash), []byte(hashPass(u.Salt, pass))) == 1
}

func addOrSetUser(cfg *Config, name, pass string) {
	st := loadUsers(cfg)
	s := randSalt()
	st.Users[name] = userRec{Salt: s, Hash: hashPass(s, pass)}
	saveUsers(cfg, st)
}

func changePass(cfg *Config, name, oldp, newp string) bool {
	if !checkUser(cfg, name, oldp) {
		return false
	}
	addOrSetUser(cfg, name, newp)
	return true
}

func listUsers(cfg *Config) []string {
	st := loadUsers(cfg)
	out := []string{}
	for n := range st.Users {
		out = append(out, n)
	}
	return out
}
