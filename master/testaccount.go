package main

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// Test accounts = throwaway login creds the device / e2e / QA test agents use to sign in on the UI
// LIKE A REAL HUMAN — never the owner's real account. Stored in the .secrets.json vault as
// TEST_ACCOUNT_<n>_EMAIL / TEST_ACCOUNT_<n>_PASSWORD so an agent can read the EXACT password.
// (The "inserts the password wrong" bug was the agent guessing / mangling it; now there is one
// canonical, owner-set value the agent reads verbatim — see jobs/e2e_test.md + jobs/qa.md.)

type TestAcct struct {
	Slot     string `json:"slot"` // "1", "2", ...
	Email    string `json:"email"`
	HasPass  bool   `json:"hasPass"`
	Backend  string `json:"backend"`            // which env it works against (staging/prod) — informational
	Password string `json:"password,omitempty"` // only filled on reveal
}

// secretSet writes one key into the vault, preserving every other key + 0600 perms.
func secretSet(cfg *Config, key, val string) error {
	path := cfg.resolve(cfg.SecretsPath)
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	if val == "" {
		delete(m, key)
	} else {
		m[key] = val
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// testAccounts lists every TEST_ACCOUNT_<n> pair in the vault (password masked unless reveal=true).
func testAccounts(cfg *Config, reveal bool) []TestAcct {
	b, err := os.ReadFile(cfg.resolve(cfg.SecretsPath))
	if err != nil {
		return []TestAcct{}
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return []TestAcct{}
	}
	slots := map[string]bool{}
	for k := range m {
		if strings.HasPrefix(k, "TEST_ACCOUNT_") && strings.HasSuffix(k, "_EMAIL") {
			slots[strings.TrimSuffix(strings.TrimPrefix(k, "TEST_ACCOUNT_"), "_EMAIL")] = true
		}
	}
	keys := make([]string, 0, len(slots))
	for s := range slots {
		keys = append(keys, s)
	}
	sort.Strings(keys)
	out := []TestAcct{}
	for _, s := range keys {
		email, _ := m["TEST_ACCOUNT_"+s+"_EMAIL"].(string)
		pass, _ := m["TEST_ACCOUNT_"+s+"_PASSWORD"].(string)
		backend, _ := m["TEST_ACCOUNT_"+s+"_BACKEND"].(string)
		ta := TestAcct{Slot: s, Email: email, HasPass: pass != "", Backend: backend}
		if reveal {
			ta.Password = pass
		}
		out = append(out, ta)
	}
	return out
}

// saveTestAccount writes/updates one slot. An empty password leaves the existing one untouched.
func saveTestAccount(cfg *Config, slot, email, pass, backend string) {
	slot = strings.TrimSpace(slot)
	if slot == "" {
		return
	}
	_ = secretSet(cfg, "TEST_ACCOUNT_"+slot+"_EMAIL", strings.TrimSpace(email))
	if strings.TrimSpace(pass) != "" {
		_ = secretSet(cfg, "TEST_ACCOUNT_"+slot+"_PASSWORD", pass) // do NOT trim — keep the password EXACTLY as typed
	}
	_ = secretSet(cfg, "TEST_ACCOUNT_"+slot+"_BACKEND", strings.TrimSpace(backend))
}
