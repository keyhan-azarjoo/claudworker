package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"time"
)

// Self-healing OAuth: when a Claude profile's access token has EXPIRED, refresh it via the OAuth
// endpoint and write the new creds BACK to the same macOS keychain slot the CLI reads — so the CLI
// and the dashboard stay in sync (this is exactly what the CLI does itself on a refresh). Without
// this, a PAUSED account (which never runs, so the CLI never refreshes it) shows "usage n/a" forever
// once its ~1h access token lapses. Rate-limit aware: on 429 we back off instead of hammering.
const (
	// PUBLIC Claude Code OAuth client id — shipped in the CLI itself, not a secret. gitleaks:allow
	oauthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e" // gitleaks:allow
	oauthTokenURL = "https://console.anthropic.com/v1/oauth/token"
)

// keychainSvc returns the keychain service name for a profile (default slot, or the per-profile
// "<...>-<first8 sha256(absDir)>" slot used when CLAUDE_CONFIG_DIR is set).
func keychainSvc(configDir string) string {
	if configDir == "" {
		return "Claude Code-credentials"
	}
	h := sha256.Sum256([]byte(expandHome(configDir)))
	return "Claude Code-credentials-" + hex.EncodeToString(h[:])[:8]
}

// keychainCreds reads a profile's full OAuth creds from the keychain.
func keychainCreds(configDir string) (token, refresh string, expMs int64, ok bool) {
	out, err := exec.Command("security", "find-generic-password", "-s", keychainSvc(configDir), "-w").Output()
	if err != nil {
		return "", "", 0, false
	}
	var m struct {
		O struct {
			AccessToken  string  `json:"accessToken"`
			RefreshToken string  `json:"refreshToken"`
			ExpiresAt    float64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(out, &m) != nil {
		return "", "", 0, false
	}
	return m.O.AccessToken, m.O.RefreshToken, int64(m.O.ExpiresAt), m.O.AccessToken != ""
}

var (
	refreshBackoff = map[string]time.Time{} // keychain svc -> don't-retry-until (rate-limit cool-down)
	refreshMu      sync.Mutex
)

// refreshBackedOff reports whether we're in a cool-down for this profile (after a 429/failure).
func refreshBackedOff(configDir string) bool {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	t, ok := refreshBackoff[keychainSvc(configDir)]
	return ok && time.Now().Before(t)
}

func setRefreshBackoff(svc string, d time.Duration) {
	refreshMu.Lock()
	refreshBackoff[svc] = time.Now().Add(d)
	refreshMu.Unlock()
}

// claudeRefresh refreshes an expired profile token and writes the new creds back to the keychain.
// Returns the fresh access token, or "" if it couldn't (no refresh token / rate-limited / error).
func claudeRefresh(configDir string) string {
	svc := keychainSvc(configDir)
	if refreshBackedOff(configDir) {
		return ""
	}
	_, refresh, _, ok := keychainCreds(configDir)
	if !ok || refresh == "" {
		return ""
	}
	body := fmt.Sprintf(`{"grant_type":"refresh_token","refresh_token":%q,"client_id":%q}`, refresh, oauthClientID)
	req, err := http.NewRequest("POST", oauthTokenURL, strings.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		setRefreshBackoff(svc, 5*time.Minute)
		return ""
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 429 {
		setRefreshBackoff(svc, 30*time.Minute) // rate-limited — cool down, retry later
		return ""
	}
	if resp.StatusCode != 200 {
		setRefreshBackoff(svc, 15*time.Minute) // e.g. 401 = refresh token dead → needs re-login
		return ""
	}
	var tr struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		ExpiresIn    float64 `json:"expires_in"`
	}
	if json.Unmarshal(rb, &tr) != nil || tr.AccessToken == "" {
		return ""
	}
	writeKeychainCreds(svc, configDir, tr.AccessToken, tr.RefreshToken, tr.ExpiresIn)
	return tr.AccessToken
}

// writeKeychainCreds merges the new tokens into the existing creds JSON and writes it back in place,
// preserving every other field (scopes, subscriptionType, …) the CLI relies on.
func writeKeychainCreds(svc, configDir, access, refresh string, expiresIn float64) {
	raw, err := exec.Command("security", "find-generic-password", "-s", svc, "-w").Output()
	if err != nil {
		return
	}
	var creds map[string]any
	if json.Unmarshal(raw, &creds) != nil {
		return
	}
	oauth, _ := creds["claudeAiOauth"].(map[string]any)
	if oauth == nil {
		oauth = map[string]any{}
	}
	oauth["accessToken"] = access
	if refresh != "" {
		oauth["refreshToken"] = refresh
	}
	if expiresIn > 0 {
		oauth["expiresAt"] = float64(time.Now().UnixMilli()) + expiresIn*1000
	}
	creds["claudeAiOauth"] = oauth
	nb, err := json.Marshal(creds)
	if err != nil {
		return
	}
	acct := "keyhanazarjoo"
	if u, e := user.Current(); e == nil && u.Username != "" {
		acct = u.Username
	}
	_ = exec.Command("security", "add-generic-password", "-U", "-s", svc, "-a", acct, "-w", string(nb)).Run()
}

// freshClaudeToken returns a USABLE access token for a profile: the keychain token if still valid,
// otherwise a freshly-refreshed one (write-back keeps the CLI in sync). "" if it can't get one.
func freshClaudeToken(configDir string) string {
	token, _, expMs, ok := keychainCreds(configDir)
	if ok && token != "" && (expMs == 0 || time.Now().UnixMilli() < expMs-60_000) {
		return token // still valid (>60s headroom)
	}
	if t := claudeRefresh(configDir); t != "" {
		return t // refreshed
	}
	return "" // expired + couldn't refresh → don't hand back a dead token (would just 429 the usage API)
}

// tokenExpiredUnrefreshable reports an account whose token has lapsed AND can't currently refresh
// (rate-limited or refresh-token dead) — i.e. it really needs a re-login on the site.
func tokenExpiredUnrefreshable(configDir string) bool {
	_, refresh, expMs, ok := keychainCreds(configDir)
	if !ok {
		return false
	}
	expired := expMs != 0 && time.Now().UnixMilli() >= expMs
	return expired && (refresh == "" || refreshBackedOff(configDir))
}
