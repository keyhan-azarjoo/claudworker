package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// secret reads one key from the .secrets.json vault.
func secret(cfg *Config, key string) string {
	b, err := os.ReadFile(cfg.resolve(cfg.SecretsPath))
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

type JiraItem struct {
	Key           string `json:"key"`
	Summary       string `json:"summary"`
	URL           string `json:"url"`
	Status        string `json:"status"`
	ParentKey     string `json:"parentKey,omitempty"`
	ParentSummary string `json:"parentSummary,omitempty"`
	Created       string `json:"created,omitempty"`  // when the task was created
	Updated       string `json:"updated,omitempty"`  // last activity (≈ start once In Progress)
	Resolved      string `json:"resolved,omitempty"` // when it finished (Done)
}

// TaskNode is one MAIN task (epic/story/standalone) with its sub-tasks + progress, for the
// dashboard's hierarchical task view.
type TaskNode struct {
	Key      string     `json:"key"`
	Summary  string     `json:"summary"`
	URL      string     `json:"url"`
	Children []JiraItem `json:"children"` // active (in-progress/to-do) sub-tasks
	Done     int        `json:"done"`     // sub-tasks finished
	Total    int        `json:"total"`    // sub-tasks total (done + active, among fetched)
	Pct      int        `json:"pct"`      // progress %
	Working  bool       `json:"working"`  // any child (or itself) in progress now
}

var (
	nhCache   []JiraItem
	nhAt      time.Time
	nhMu      sync.Mutex
	nhRefresh int32
)

// needsHuman returns the cached needs-human issues INSTANTLY (stale-while-revalidate).
func needsHuman(cfg *Config) []JiraItem {
	nhMu.Lock()
	c, age := nhCache, time.Since(nhAt)
	nhMu.Unlock()
	if !nhAt.IsZero() && age < 60*time.Second {
		return c
	}
	go refreshNeedsHuman(cfg)
	return c
}

func refreshNeedsHuman(cfg *Config) []JiraItem {
	if !atomic.CompareAndSwapInt32(&nhRefresh, 0, 1) {
		nhMu.Lock()
		c := nhCache
		nhMu.Unlock()
		return c
	}
	defer atomic.StoreInt32(&nhRefresh, 0)
	email := secret(cfg, "ATLASSIAN_EMAIL")
	token := secret(cfg, "ATLASSIAN_TOKEN")
	site := secret(cfg, "ATLASSIAN_SITE")
	if email == "" || token == "" || site == "" {
		return nil
	}
	jql := projJQL(cfg, "project=SCRUM AND labels in (needs-human, blocked) AND statusCategory!=Done ORDER BY priority DESC")
	endpoint := "https://" + site + "/rest/api/3/search/jql?" +
		url.Values{"jql": {jql}, "fields": {"summary,status"}, "maxResults": {"30"}}.Encode()
	req, _ := http.NewRequest("GET", endpoint, nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(email+":"+token)))
	req.Header.Set("Accept", "application/json")
	cli := &http.Client{Timeout: 12 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
				Status  struct {
					Name string `json:"name"`
				} `json:"status"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	items := make([]JiraItem, 0, len(out.Issues))
	for _, i := range out.Issues {
		items = append(items, JiraItem{
			Key: i.Key, Summary: i.Fields.Summary, Status: i.Fields.Status.Name,
			URL: "https://" + site + "/browse/" + i.Key,
		})
	}
	nhMu.Lock()
	nhCache, nhAt = items, time.Now()
	nhMu.Unlock()
	return items
}

// PathStat is a progress bucket for the top-of-page tracker.
type PathStat struct {
	Label    string `json:"label"`
	Total    int    `json:"total"`
	Done     int    `json:"done"`
	Pct      int    `json:"pct"`
	ETAHours int    `json:"etaHours"`
}

var (
	psCache []PathStat
	psAt    time.Time
	psMu    sync.Mutex
)

func jiraPOST(cfg *Config, path string, payload any) bool {
	email := secret(cfg, "ATLASSIAN_EMAIL")
	token := secret(cfg, "ATLASSIAN_TOKEN")
	site := secret(cfg, "ATLASSIAN_SITE")
	if email == "" || token == "" || site == "" {
		return false
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "https://"+site+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(email+":"+token)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 300
}

// jiraPUT is jiraPOST with the PUT verb (for issue edits, e.g. label changes).
func jiraPUT(cfg *Config, path string, payload any) bool {
	email := secret(cfg, "ATLASSIAN_EMAIL")
	token := secret(cfg, "ATLASSIAN_TOKEN")
	site := secret(cfg, "ATLASSIAN_SITE")
	if email == "" || token == "" || site == "" {
		return false
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest("PUT", "https://"+site+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(email+":"+token)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 300
}

// invalidateTickets forces the next tickets() call to re-query Jira (so an approval
// reflects immediately instead of waiting out the 45s cache).
func invalidateTickets() {
	tkMu.Lock()
	tkAt = time.Time{}
	tkMu.Unlock()
}

// activateTicket = the owner approved a "needs you" item. The approval list is built from
// the LABELS needs-human/approval/blocked, so approving must REMOVE those labels (that's what
// actually drops it off the list) — a status transition alone left the labels on and the count
// never changed. Also tag it 'approved' and nudge it forward, then bust the cache.
func activateTicket(cfg *Config, key string) bool {
	ok := jiraPUT(cfg, "/rest/api/3/issue/"+key, map[string]any{
		"update": map[string]any{
			"labels": []map[string]string{
				{"remove": "needs-human"}, {"remove": "approval"}, {"remove": "blocked"},
				{"add": "approved"},
			},
		},
	})
	// Best-effort: move it forward so the fleet picks it up (workflows vary; ignore failure).
	jiraPOST(cfg, "/rest/api/3/issue/"+key+"/transitions", map[string]any{"transition": map[string]string{"id": "21"}})
	invalidateTickets()
	return ok
}

// activateHardwareWaiting removes the wait-for-hardware labels from every hardware-waiting ticket
// (and tags it 'hardware-ready') so the fleet immediately starts working them — used when a device
// is plugged in/connected. Returns how many it released.
func activateHardwareWaiting(cfg *Config) int {
	b, ok := jiraGET(cfg, "/rest/api/3/search/jql?"+url.Values{
		"jql":        {projJQL(cfg, "project=SCRUM AND statusCategory!=Done AND labels in (wait-for-hardware-test, hardware-wait)")},
		"fields":     {"key"},
		"maxResults": {"40"},
	}.Encode())
	if !ok {
		return 0
	}
	var out struct {
		Issues []struct {
			Key string `json:"key"`
		} `json:"issues"`
	}
	if json.Unmarshal(b, &out) != nil {
		return 0
	}
	n := 0
	for _, i := range out.Issues {
		if jiraPUT(cfg, "/rest/api/3/issue/"+i.Key, map[string]any{
			"update": map[string]any{"labels": []map[string]string{
				{"remove": "wait-for-hardware-test"}, {"remove": "hardware-wait"}, {"add": "hardware-ready"},
			}},
		}) {
			jiraPOST(cfg, "/rest/api/3/issue/"+i.Key+"/transitions", map[string]any{"transition": map[string]string{"id": "21"}})
			n++
		}
	}
	if n > 0 {
		invalidateTickets()
	}
	return n
}

func jiraGET(cfg *Config, path string) ([]byte, bool) {
	email := secret(cfg, "ATLASSIAN_EMAIL")
	token := secret(cfg, "ATLASSIAN_TOKEN")
	site := secret(cfg, "ATLASSIAN_SITE")
	if email == "" || token == "" || site == "" {
		return nil, false
	}
	req, _ := http.NewRequest("GET", "https://"+site+path, nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(email+":"+token)))
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, false
	}
	b, _ := io.ReadAll(resp.Body)
	return b, true
}

// jqlTotal returns the REAL issue count for a JQL. The new Jira /search/jql endpoint dropped the
// `total` field, so use the dedicated approximate-count endpoint (accurate for board-size counts).
func jqlTotal(cfg *Config, jql string) int {
	email := secret(cfg, "ATLASSIAN_EMAIL")
	token := secret(cfg, "ATLASSIAN_TOKEN")
	site := secret(cfg, "ATLASSIAN_SITE")
	if email == "" || token == "" || site == "" {
		return -1
	}
	body, _ := json.Marshal(map[string]string{"jql": projJQL(cfg, jql)})
	req, _ := http.NewRequest("POST", "https://"+site+"/rest/api/3/search/approximate-count", bytes.NewReader(body))
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(email+":"+token)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var r struct {
		Count int `json:"count"`
	}
	if json.Unmarshal(b, &r) != nil {
		return -1
	}
	return r.Count
}

var psRefresh int32

// pathStats returns cached progress INSTANTLY (stale-while-revalidate; never blocks the SSE).
func pathStats(cfg *Config) []PathStat {
	psMu.Lock()
	c, age := psCache, time.Since(psAt)
	psMu.Unlock()
	if c != nil && age < 60*time.Second {
		return c
	}
	go refreshPathStats(cfg)
	return c
}

func refreshPathStats(cfg *Config) []PathStat {
	if !atomic.CompareAndSwapInt32(&psRefresh, 0, 1) {
		psMu.Lock()
		c := psCache
		psMu.Unlock()
		return c
	}
	defer atomic.StoreInt32(&psRefresh, 0)
	buckets := []struct{ label, base string }{
		{"Path", "project=SCRUM"},
		{"Human test", "project=SCRUM AND labels in (wait-for-human-test, human-test, e2e, visual)"},
		{"Hardware", "project=SCRUM AND labels in (hardware, firmware, pcb, wait-for-hardware-test, uk-compliance)"},
	}
	out := []PathStat{}
	for _, b := range buckets {
		total := jqlTotal(cfg, b.base)
		done := jqlTotal(cfg, b.base+" AND statusCategory=Done")
		if total < 0 {
			continue
		}
		pct := 0
		if total > 0 && done >= 0 {
			pct = done * 100 / total
		}
		remaining := total - done
		if remaining < 0 {
			remaining = 0
		}
		out = append(out, PathStat{Label: b.label, Total: total, Done: done, Pct: pct, ETAHours: (remaining*20 + 59) / 60})
	}
	if out != nil {
		psMu.Lock()
		psCache, psAt = out, time.Now()
		psMu.Unlock()
	}
	return out
}

var (
	tkCache   map[string]any
	tkAt      time.Time
	tkMu      sync.Mutex
	tkRefresh int32
)

// tickets returns the cached board INSTANTLY (never blocks the SSE on Jira). Stale → async refresh.
func tickets(cfg *Config) map[string]any {
	tkMu.Lock()
	c, age := tkCache, time.Since(tkAt)
	tkMu.Unlock()
	if c != nil && age < 45*time.Second {
		return c
	}
	go refreshTickets(cfg)
	if c == nil {
		return map[string]any{} // cold: empty now, fills in next tick (never block the SSE)
	}
	return c
}

func refreshTickets(cfg *Config) map[string]any {
	if !atomic.CompareAndSwapInt32(&tkRefresh, 0, 1) {
		tkMu.Lock()
		c := tkCache
		tkMu.Unlock()
		return c
	}
	defer atomic.StoreInt32(&tkRefresh, 0)
	site := secret(cfg, "ATLASSIAN_SITE")
	fetchN := func(jql string, max int) []JiraItem {
		jql = projJQL(cfg, jql)
		b, ok := jiraGET(cfg, "/rest/api/3/search/jql?"+url.Values{"jql": {jql}, "fields": {"summary,status,priority,parent,created,updated,resolutiondate"}, "maxResults": {strconv.Itoa(max)}}.Encode())
		if !ok {
			return nil
		}
		var out struct {
			Issues []struct {
				Key    string `json:"key"`
				Fields struct {
					Summary  string `json:"summary"`
					Status   struct{ Name string `json:"name"` } `json:"status"`
					Priority struct{ Name string `json:"name"` } `json:"priority"`
					Created  string `json:"created"`
					Updated  string `json:"updated"`
					Resolved string `json:"resolutiondate"`
					Parent   struct {
						Key    string `json:"key"`
						Fields struct {
							Summary string `json:"summary"`
						} `json:"fields"`
					} `json:"parent"`
				} `json:"fields"`
			} `json:"issues"`
		}
		if json.Unmarshal(b, &out) != nil {
			return nil
		}
		items := []JiraItem{}
		for _, i := range out.Issues {
			items = append(items, JiraItem{Key: i.Key, Summary: i.Fields.Summary, Status: i.Fields.Status.Name,
				URL: "https://" + site + "/browse/" + i.Key, ParentKey: i.Fields.Parent.Key, ParentSummary: i.Fields.Parent.Fields.Summary,
				Created: i.Fields.Created, Updated: i.Fields.Updated, Resolved: i.Fields.Resolved})
		}
		return items
	}
	fetch := func(jql string) []JiraItem { return fetchN(jql, 60) }
	// "In progress" = work items actually being worked. EXCLUDE Epics (containers, not a worked ticket)
	// and the Cancel status (abandoned but parked in the In-Progress category by the Jira workflow) —
	// otherwise the count is wildly inflated (e.g. 144 = 14 epics + 8 cancelled + 23 in-review + ~99 real).
	working := fetch(`project=SCRUM AND statusCategory="In Progress" AND issuetype != Epic AND status != Cancel ORDER BY priority DESC`)
	queue := fetch("project=SCRUM AND statusCategory=\"To Do\" ORDER BY priority DESC, created ASC")
	doneList := fetch("project=SCRUM AND statusCategory=Done ORDER BY updated DESC")
	bugs := fetch("project=SCRUM AND statusCategory!=Done AND labels in (bug, sentry, e2e, visual, regression) ORDER BY priority DESC")
	// Only surface approvals that genuinely need the owner: an explicit needs-human/approval
	// label AND a real priority (Highest/High/Medium). Dropped 'blocked' (that's a dependency
	// block, not an approval request) and Low/Lowest, so silly stuff doesn't sit in "Needs you".
	approval := fetch("project=SCRUM AND statusCategory!=Done AND labels in (needs-human, approval) AND priority in (Highest, High, Medium) ORDER BY priority DESC")
	hwWait := fetch("project=SCRUM AND statusCategory!=Done AND labels in (wait-for-hardware-test, hardware-wait) ORDER BY priority DESC")
	humanTest := fetch("project=SCRUM AND statusCategory!=Done AND labels in (wait-for-human-test, human-test) ORDER BY priority DESC")
	// breakdown lists for the Backlog page categories (what makes up the In-Progress category)
	inReview := fetch(`project=SCRUM AND status="In Review" ORDER BY updated DESC`)
	epicsList := fetch(`project=SCRUM AND issuetype=Epic AND statusCategory="In Progress" ORDER BY updated DESC`)
	cancelledList := fetch(`project=SCRUM AND status=Cancel ORDER BY updated DESC`)
	// Build the MAIN-task tree: group active sub-tasks under their parent, with progress.
	// A task with no parent is itself a main task (a node with no children).
	nodes := map[string]*TaskNode{}
	order := []string{}
	node := func(key, summary, u string) *TaskNode {
		n := nodes[key]
		if n == nil {
			n = &TaskNode{Key: key, Summary: summary, URL: u}
			nodes[key] = n
			order = append(order, key)
		}
		if n.Summary == "" {
			n.Summary = summary
		}
		return n
	}
	for _, it := range append(append([]JiraItem{}, working...), queue...) {
		if it.ParentKey != "" {
			p := node(it.ParentKey, it.ParentSummary, "https://"+site+"/browse/"+it.ParentKey)
			p.Children = append(p.Children, it)
			p.Total++
			if it.Status == "In Progress" || strings.EqualFold(it.Status, "in progress") {
				p.Working = true
			}
		} else {
			n := node(it.Key, it.Summary, it.URL)
			n.Total++
			if it.Status == "In Progress" {
				n.Working = true
			}
		}
	}
	for _, it := range doneList { // done children count toward their parent's progress
		if it.ParentKey != "" {
			if n := nodes[it.ParentKey]; n != nil {
				n.Done++
				n.Total++
			}
		}
	}
	tree := make([]*TaskNode, 0, len(order))
	for _, k := range order {
		n := nodes[k]
		if n.Total > 0 {
			n.Pct = n.Done * 100 / n.Total
		}
		tree = append(tree, n)
	}
	// parents with active work first, then by progress
	sort.Slice(tree, func(i, j int) bool {
		if tree[i].Working != tree[j].Working {
			return tree[i].Working
		}
		return tree[i].Pct < tree[j].Pct
	})

	res := map[string]any{
		"tree":       tree,
		"total":      jqlTotal(cfg, "project=SCRUM"),
		"doneCnt":    jqlTotal(cfg, "project=SCRUM AND statusCategory=Done"),
		// real in-progress DEV work only (exclude epics=containers, Cancel=abandoned, In Review=its own stage)
		"workingCnt":  jqlTotal(cfg, `project=SCRUM AND statusCategory="In Progress" AND issuetype != Epic AND status not in (Cancel, "In Review")`),
		"inReviewCnt": jqlTotal(cfg, `project=SCRUM AND status="In Review"`),
		"epicCnt":     jqlTotal(cfg, `project=SCRUM AND issuetype=Epic AND statusCategory="In Progress"`),
		"cancelledCnt": jqlTotal(cfg, `project=SCRUM AND status=Cancel`),
		"staleCnt":    jqlTotal(cfg, `project=SCRUM AND statusCategory="In Progress" AND issuetype != Epic AND status not in (Cancel, "In Review") AND updated <= "-3d"`),
		"inReview":    inReview,
		"epics":       epicsList,
		"cancelled":   cancelledList,
		"queueCnt":   jqlTotal(cfg, "project=SCRUM AND statusCategory=\"To Do\""),
		"working":    working,
		"queue":      queue,
		"done":       doneList,
		"bugs":       bugs,
		"bugsCnt":    jqlTotal(cfg, "project=SCRUM AND statusCategory!=Done AND labels in (bug, sentry, e2e, visual, regression)"),
		"approval":   approval,
		"hwWait":     hwWait,
		"humanTest":  humanTest,
		// NOTE: the full "all" backlog is NOT streamed (it's 1000+ items = ~500KB/frame). It's served
		// on demand via /api/tickets/all when the owner opens the All view.
	}
	tkMu.Lock()
	tkCache, tkAt = res, time.Now()
	tkMu.Unlock()
	return res
}

// allTickets cache: the full backlog, paginated, newest-activity first. Refreshed at most every
// 5 min (it changes slowly and is large), independent of the 45s working/queue refresh.
var (
	allTkCache []JiraItem
	allTkAt    time.Time
	allTkMu    sync.Mutex
	allTkBusy  int32
)

// allTicketsNow returns the full backlog (on-demand for the /api/tickets/all endpoint).
func allTicketsNow(cfg *Config) []JiraItem {
	return allTicketsCached(cfg, secret(cfg, "ATLASSIAN_SITE"))
}

// allTicketsCached returns the cached full ticket list INSTANTLY; refreshes in the background.
func allTicketsCached(cfg *Config, site string) []JiraItem {
	allTkMu.Lock()
	c, age := allTkCache, time.Since(allTkAt)
	allTkMu.Unlock()
	if c != nil && age < 5*time.Minute {
		return c
	}
	if atomic.CompareAndSwapInt32(&allTkBusy, 0, 1) {
		go func() {
			defer atomic.StoreInt32(&allTkBusy, 0)
			items := jiraSearchPaged(cfg, site, projJQL(cfg, "project=SCRUM ORDER BY updated DESC"), 1200)
			if len(items) > 0 {
				allTkMu.Lock()
				allTkCache, allTkAt = items, time.Now()
				allTkMu.Unlock()
			}
		}()
	}
	return c // may be nil on the very first call; fills in within a few seconds
}

// jiraSearchPaged pulls up to `cap` issues across pages (Jira caps each page at 100), following
// nextPageToken. Returns them with Created/Updated/Resolved populated.
func jiraSearchPaged(cfg *Config, site, jql string, limit int) []JiraItem {
	out := []JiraItem{}
	token := ""
	for len(out) < limit {
		q := url.Values{"jql": {jql}, "fields": {"summary,status,priority,parent,created,updated,resolutiondate"}, "maxResults": {"100"}}
		if token != "" {
			q.Set("nextPageToken", token)
		}
		b, ok := jiraGET(cfg, "/rest/api/3/search/jql?"+q.Encode())
		if !ok {
			break
		}
		var resp struct {
			NextPageToken string `json:"nextPageToken"`
			Issues        []struct {
				Key    string `json:"key"`
				Fields struct {
					Summary  string                  `json:"summary"`
					Status   struct{ Name string `json:"name"` } `json:"status"`
					Created  string                  `json:"created"`
					Updated  string                  `json:"updated"`
					Resolved string                  `json:"resolutiondate"`
					Parent   struct {
						Key    string `json:"key"`
						Fields struct {
							Summary string `json:"summary"`
						} `json:"fields"`
					} `json:"parent"`
				} `json:"fields"`
			} `json:"issues"`
		}
		if json.Unmarshal(b, &resp) != nil || len(resp.Issues) == 0 {
			break
		}
		for _, i := range resp.Issues {
			out = append(out, JiraItem{Key: i.Key, Summary: i.Fields.Summary, Status: i.Fields.Status.Name,
				URL: "https://" + site + "/browse/" + i.Key, ParentKey: i.Fields.Parent.Key, ParentSummary: i.Fields.Parent.Fields.Summary,
				Created: i.Fields.Created, Updated: i.Fields.Updated, Resolved: i.Fields.Resolved})
		}
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	return out
}

// adfText flattens an Atlassian Document Format (ADF) node tree into plain text.
func adfText(v any) string {
	var b strings.Builder
	var walk func(n any)
	walk = func(n any) {
		switch t := n.(type) {
		case map[string]any:
			if t["type"] == "text" {
				if s, ok := t["text"].(string); ok {
					b.WriteString(s)
				}
			}
			if c, ok := t["content"].([]any); ok {
				for _, x := range c {
					walk(x)
				}
			}
			if t["type"] == "paragraph" || t["type"] == "heading" || t["type"] == "listItem" || t["type"] == "rule" {
				b.WriteString("\n")
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)
	return strings.TrimSpace(b.String())
}

// ticketDetail fetches a single issue's full detail (summary, description, status, labels, comments).
func ticketDetail(cfg *Config, key string) map[string]any {
	site := secret(cfg, "ATLASSIAN_SITE")
	b, ok := jiraGET(cfg, "/rest/api/3/issue/"+key+"?fields=summary,description,status,labels,priority,comment")
	if !ok {
		return map[string]any{"key": key, "error": "could not load"}
	}
	var r struct {
		Fields struct {
			Summary     string `json:"summary"`
			Description any    `json:"description"`
			Status      struct{ Name string `json:"name"` } `json:"status"`
			Priority    struct{ Name string `json:"name"` } `json:"priority"`
			Labels      []string `json:"labels"`
			Comment     struct {
				Comments []struct {
					Author struct{ DisplayName string `json:"displayName"` } `json:"author"`
					Body   any `json:"body"`
				} `json:"comments"`
			} `json:"comment"`
		} `json:"fields"`
	}
	if json.Unmarshal(b, &r) != nil {
		return map[string]any{"key": key, "error": "parse"}
	}
	comments := []map[string]string{}
	for _, c := range r.Fields.Comment.Comments {
		comments = append(comments, map[string]string{"author": c.Author.DisplayName, "text": adfText(c.Body)})
	}
	return map[string]any{
		"key": key, "summary": r.Fields.Summary, "status": r.Fields.Status.Name,
		"priority": r.Fields.Priority.Name, "labels": r.Fields.Labels,
		"description": adfText(r.Fields.Description), "comments": comments,
		"url": "https://" + site + "/browse/" + key,
	}
}

// devices reads the curated device list and overlays LIVE connected state (re-detected every ~8s).
func devices(cfg *Config) []map[string]any {
	b, err := os.ReadFile(cfg.resolve(cfg.LogDir) + "/devices.json")
	if err != nil {
		return nil
	}
	var ds []map[string]any
	if json.Unmarshal(b, &ds) != nil {
		return nil
	}
	claims := deviceClaims(cfg)
	for _, d := range ds {
		if d["kind"] == "sim" {
			d["connected"] = true
		} else {
			reach, _ := d["reach"].(string)
			d["connected"] = deviceConnected(reach)
		}
		// connection metadata for the Devices table (how to connect + address)
		m, addr, how := deviceConn(d)
		d["connMethod"], d["connAddress"], d["connHow"] = m, addr, how
		// expose whether a password is stored, but NEVER stream the password itself
		if p, _ := d["pass"].(string); p != "" {
			d["hasPass"] = true
		}
		delete(d, "pass")
		// overlay the background deep-check result (authoritative connected status + freshness)
		if dn, _ := d["name"].(string); dn != "" && d["kind"] != "sim" {
			if c := devCheckGet(dn); c != nil {
				if okv, has := c["ok"].(bool); has {
					d["connected"] = okv
				}
				d["checkDetail"], d["checkedAt"] = c["detail"], c["ts"]
			}
		}
		// overlay "in use by another agent" from the shared coordination board
		reach, _ := d["reach"].(string)
		name, _ := d["name"].(string)
		for tok, who := range claims {
			if strings.Contains(reach, tok) || strings.Contains(name, tok) {
				d["inUse"] = true
				d["usedBy"] = who
				break
			}
		}
	}
	return ds
}
