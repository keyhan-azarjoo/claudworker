package main

// Integrations = the "Connect your own accounts" layer for the account page.
//
// Goal: anyone can clone this fleet, open the dashboard, and connect THEIR OWN Jira / Confluence /
// Sentry / backend / GitHub / Telegram accounts from the UI — no editing JSON by hand. Every value
// is stored locally: non-secret settings (site, project key, org, space, URLs) and secret tokens BOTH
// go in the gitignored secrets vault via secretSet(), so nothing ever reaches the published repo.
//
// This file owns the catalog (what services exist + which vault key each field maps to), the
// "connected?" computation, and live connection tests. The HTTP handlers live in server.go and just
// call these functions.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// IntegrationField is one credential/setting input rendered on the Integrations page.
type IntegrationField struct {
	Key         string `json:"key"`         // vault key this field reads/writes
	Label       string `json:"label"`       // UI label
	Secret      bool   `json:"secret"`      // mask in the UI; only sent back on explicit reveal
	Optional    bool   `json:"optional"`    // not required for the service to count as "connected"
	Placeholder string `json:"placeholder"` // example value
	Help        string `json:"help"`        // one-line hint
	Value       string `json:"value"`       // current value (blank for secrets — use Set instead)
	Set         bool   `json:"set"`         // secret fields: does a value already exist?
}

// Integration is one external service the user connects with their OWN account.
type Integration struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Icon      string             `json:"icon"`
	Blurb     string             `json:"blurb"`
	DocURL    string             `json:"docUrl"` // where to get the token
	Fields    []IntegrationField `json:"fields"`
	Connected bool               `json:"connected"` // all required fields present
	Testable  bool               `json:"testable"`  // a live connection test exists
}

// integrationCatalog is the static definition of every connectable service. Adding a new service is
// just adding an entry here (+ optionally a case in integrationTest). Vault keys MUST match what the
// consuming code already reads (jira.go/sentry.go/runner.go) so the fleet picks the value up.
func integrationCatalog() []Integration {
	return []Integration{
		{
			ID: "jira", Name: "Jira", Icon: "📋", Testable: true,
			Blurb:  "Issue tracker the fleet pulls work from and reports back to. Create an API token in your Atlassian account.",
			DocURL: "https://id.atlassian.com/manage-profile/security/api-tokens",
			Fields: []IntegrationField{
				{Key: "ATLASSIAN_SITE", Label: "Site", Placeholder: "yourcompany.atlassian.net", Help: "Your Atlassian site host (no https://)."},
				{Key: "ATLASSIAN_EMAIL", Label: "Account email", Placeholder: "you@example.com", Help: "The email of the Atlassian account the token belongs to."},
				{Key: "ATLASSIAN_TOKEN", Label: "API token", Secret: true, Help: "Atlassian → Security → API tokens. Stored locally, never committed."},
				{Key: "JIRA_PROJECT", Label: "Project key", Placeholder: "SCRUM", Help: "The project key the fleet works in (e.g. SCRUM, ENG, PROJ)."},
			},
		},
		{
			ID: "confluence", Name: "Confluence", Icon: "📖", Testable: true,
			Blurb:  "Docs/wiki the fleet reads and updates. Uses the SAME Atlassian site + email + token as Jira — set those on the Jira card; here just pick the space.",
			DocURL: "https://id.atlassian.com/manage-profile/security/api-tokens",
			Fields: []IntegrationField{
				{Key: "CONFLUENCE_SPACE", Label: "Space key", Placeholder: "SD", Help: "The Confluence space key the fleet documents in.", Optional: true},
			},
		},
		{
			ID: "sentry", Name: "Sentry", Icon: "🪲", Testable: true,
			Blurb:  "Error monitoring. The reliability agent turns new Sentry issues into tickets. Create an auth token with project:read + org:read scope.",
			DocURL: "https://docs.sentry.io/account/auth-tokens/",
			Fields: []IntegrationField{
				{Key: "SENTRY_ORG", Label: "Org slug", Placeholder: "your-org", Help: "Your Sentry organization slug."},
				{Key: "SENTRY_TOKEN", Label: "Auth token", Secret: true, Help: "Sentry → Settings → Auth Tokens."},
				{Key: "SENTRY_API_BASE", Label: "API base", Placeholder: "https://sentry.io", Help: "Leave blank for sentry.io; set for self-hosted/EU (e.g. https://sentry.io).", Optional: true},
			},
		},
		{
			ID: "github", Name: "GitHub", Icon: "🐙", Testable: true,
			Blurb:  "Used for PRs, releases and gh CLI work. A fine-grained or classic PAT with repo scope.",
			DocURL: "https://github.com/settings/tokens",
			Fields: []IntegrationField{
				{Key: "GITHUB_TOKEN", Label: "Personal access token", Secret: true, Help: "github.com → Settings → Developer settings → Tokens."},
				{Key: "GITHUB_OWNER", Label: "Owner / org", Placeholder: "your-username", Help: "Default GitHub owner the agents push to.", Optional: true},
			},
		},
		{
			ID: "telegram", Name: "Telegram", Icon: "✈️", Testable: true,
			Blurb:  "Optional. The fleet sends you status pings + asks for approvals here. Create a bot with @BotFather.",
			DocURL: "https://core.telegram.org/bots#how-do-i-create-a-bot",
			Fields: []IntegrationField{
				{Key: "TELEGRAM_BOT_TOKEN", Label: "Bot token", Secret: true, Optional: true, Help: "The token @BotFather gives you."},
				{Key: "TELEGRAM_CHAT_ID", Label: "Chat ID", Placeholder: "123456789", Optional: true, Help: "Your chat/user ID the bot messages."},
			},
		},
		{
			ID: "anthropic", Name: "Claude (Anthropic)", Icon: "🤖", Testable: false,
			Blurb:  "The default engine is the Claude CLI — log accounts in on the Accounts page (no key needed). Set an API key only if you prefer billing per token instead of a CLI subscription.",
			DocURL: "https://console.anthropic.com/settings/keys",
			Fields: []IntegrationField{
				{Key: "ANTHROPIC_API_KEY", Label: "API key", Secret: true, Optional: true, Help: "Optional — only if not using the Claude CLI login."},
			},
		},
		{
			ID: "voice", Name: "Voice (Azure Speech)", Icon: "🎙️", Testable: false,
			Blurb:  "Optional. Powers the local 'Tgo' wake-word assistant (speech-to-text + text-to-speech).",
			DocURL: "https://portal.azure.com",
			Fields: []IntegrationField{
				{Key: "AZURE_SPEECH_KEY", Label: "Speech key", Secret: true, Optional: true, Help: "Azure Cognitive Services → Speech resource key."},
				{Key: "AZURE_SPEECH_REGION", Label: "Region", Placeholder: "eastus", Optional: true, Help: "The Azure region of the Speech resource."},
			},
		},
	}
}

// jiraProject returns the configured Jira project key (default "SCRUM" so existing setups are
// unchanged). New users set it on the Integrations page.
func jiraProject(cfg *Config) string {
	if p := strings.TrimSpace(secret(cfg, "JIRA_PROJECT")); p != "" {
		return p
	}
	return "SCRUM"
}

// projJQL rewrites the literal "project=SCRUM" used throughout the JQL strings to the configured
// project key. When the key is the default "SCRUM" it's a no-op, so existing setups are unchanged.
func projJQL(cfg *Config, jql string) string {
	if p := jiraProject(cfg); p != "SCRUM" {
		return strings.ReplaceAll(jql, "project=SCRUM", "project="+p)
	}
	return jql
}

// confluenceSpace returns the configured Confluence space key (default "SD" for back-compat).
func confluenceSpace(cfg *Config) string {
	if s := strings.TrimSpace(secret(cfg, "CONFLUENCE_SPACE")); s != "" {
		return s
	}
	return "SD"
}

// integrationStatus fills each catalog field with its current stored value (secrets masked) and
// computes whether the service is connected.
func integrationStatus(cfg *Config) []Integration {
	cat := integrationCatalog()
	for i := range cat {
		connected := true
		for j := range cat[i].Fields {
			f := &cat[i].Fields[j]
			val := secret(cfg, f.Key)
			if f.Secret {
				f.Set = val != ""
				f.Value = "" // never leak a secret in the status payload
			} else {
				f.Value = val
			}
			if !f.Optional && val == "" {
				connected = false
			}
		}
		// Confluence reuses the Jira/Atlassian credentials — it's connected once those + a space exist.
		if cat[i].ID == "confluence" {
			connected = secret(cfg, "ATLASSIAN_SITE") != "" && secret(cfg, "ATLASSIAN_TOKEN") != ""
		}
		cat[i].Connected = connected
	}
	return cat
}

// saveIntegration writes the posted field values into the vault. A blank SECRET value is ignored
// (so re-saving a form without re-typing the token keeps the existing one); blank non-secret values
// are cleared. Only keys that belong to a known field are accepted (whitelist).
func saveIntegration(cfg *Config, vals map[string]string) {
	allowed := map[string]bool{}
	secretKey := map[string]bool{}
	for _, it := range integrationCatalog() {
		for _, f := range it.Fields {
			allowed[f.Key] = true
			secretKey[f.Key] = f.Secret
		}
	}
	for k, v := range vals {
		if !allowed[k] {
			continue
		}
		if secretKey[k] && strings.TrimSpace(v) == "" {
			continue // keep the existing secret
		}
		_ = secretSet(cfg, k, strings.TrimSpace(v))
	}
}

// parseMonitors turns the backend health-check textarea ("Name|URL" per line, or just a URL) into
// the config's monitor list. Blank lines are skipped.
func parseMonitors(s string) []MonitorTarget {
	out := []MonitorTarget{}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		name, u := "", ln
		if i := strings.Index(ln, "|"); i >= 0 {
			name = strings.TrimSpace(ln[:i])
			u = strings.TrimSpace(ln[i+1:])
		}
		if u == "" {
			continue
		}
		if name == "" {
			name = u
		}
		out = append(out, MonitorTarget{Name: name, URL: u})
	}
	return out
}

// integrationTest does a live, read-only call to verify the stored credentials actually work.
func integrationTest(cfg *Config, id string) (bool, string) {
	cl := &http.Client{Timeout: 12 * time.Second}
	switch id {
	case "jira", "confluence":
		site := strings.TrimSpace(secret(cfg, "ATLASSIAN_SITE"))
		email := strings.TrimSpace(secret(cfg, "ATLASSIAN_EMAIL"))
		tok := secret(cfg, "ATLASSIAN_TOKEN")
		if site == "" || email == "" || tok == "" {
			return false, "Fill in site, email and API token on the Jira card first."
		}
		path := "/rest/api/3/myself"
		if id == "confluence" {
			path = "/wiki/rest/api/space?limit=1"
		}
		req, _ := http.NewRequest("GET", "https://"+site+path, nil)
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(email+":"+tok)))
		req.Header.Set("Accept", "application/json")
		resp, err := cl.Do(req)
		if err != nil {
			return false, "Connection failed: " + err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			if id == "confluence" {
				return true, "Connected to Confluence at " + site + "."
			}
			var me struct {
				DisplayName string `json:"displayName"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&me)
			return true, "Connected to Jira as " + me.DisplayName + "."
		}
		return false, fmt.Sprintf("Atlassian returned HTTP %d — check the email + token.", resp.StatusCode)
	case "sentry":
		org := strings.TrimSpace(secret(cfg, "SENTRY_ORG"))
		tok := secret(cfg, "SENTRY_TOKEN")
		if org == "" || tok == "" {
			return false, "Fill in the org slug and auth token first."
		}
		req, _ := http.NewRequest("GET", sentryBase(cfg)+"/api/0/organizations/"+org+"/", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := cl.Do(req)
		if err != nil {
			return false, "Connection failed: " + err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			return true, "Connected to Sentry org " + org + "."
		}
		return false, fmt.Sprintf("Sentry returned HTTP %d — check the org slug + token scopes.", resp.StatusCode)
	case "github":
		tok := secret(cfg, "GITHUB_TOKEN")
		if tok == "" {
			return false, "Add a personal access token first."
		}
		req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := cl.Do(req)
		if err != nil {
			return false, "Connection failed: " + err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			var u struct {
				Login string `json:"login"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&u)
			return true, "Connected to GitHub as " + u.Login + "."
		}
		return false, fmt.Sprintf("GitHub returned HTTP %d — check the token.", resp.StatusCode)
	case "telegram":
		tok := secret(cfg, "TELEGRAM_BOT_TOKEN")
		if tok == "" {
			return false, "Add a bot token first."
		}
		resp, err := cl.Get("https://api.telegram.org/bot" + tok + "/getMe")
		if err != nil {
			return false, "Connection failed: " + err.Error()
		}
		defer resp.Body.Close()
		var r struct {
			OK     bool `json:"ok"`
			Result struct {
				Username string `json:"username"`
			} `json:"result"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&r)
		if r.OK {
			return true, "Connected to bot @" + r.Result.Username + "."
		}
		return false, "Telegram rejected the bot token."
	}
	return false, "No live test for this service — values are saved."
}
