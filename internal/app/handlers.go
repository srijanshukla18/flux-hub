package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// focusFromRequest resolves either ?pr= / ?sha= or a direct object query
// (?kind=HelmRelease|Kustomization|GitRepository&ns=...&name=...).
func (a *App) focusFromRequest(r *http.Request) *FocusResolution {
	q := r.URL.Query()
	pr := strings.TrimSpace(q.Get("pr"))
	sha := strings.TrimSpace(q.Get("sha"))
	kind := strings.TrimSpace(q.Get("kind"))
	ns := strings.TrimSpace(q.Get("ns"))
	name := strings.TrimSpace(q.Get("name"))

	if pr == "" && sha == "" && kind == "" && ns == "" && name == "" {
		return nil
	}

	if pr == "" && sha == "" && (kind != "" || ns != "" || name != "") {
		return directFocusFromQuery(kind, ns, name)
	}

	if a.github.Token == "" || a.github.Repo == "" {
		source := "PR #" + pr
		if pr == "" {
			source = "commit " + shortSHA(sha)
		}
		res := &FocusResolution{
			Source: source,
			Error:  "GitHub token and repo are required (set GITHUB_TOKEN and GITHUB_REPO)",
		}
		if pr != "" {
			res.RawParam = "pr=" + url.QueryEscape(pr)
		} else {
			res.RawParam = "sha=" + url.QueryEscape(sha)
		}
		return res
	}

	if pr != "" {
		n, err := strconv.Atoi(pr)
		if err != nil || n <= 0 {
			return &FocusResolution{
				RawParam: "pr=" + url.QueryEscape(pr),
				Source:   "PR #" + pr,
				Error:    "invalid PR number",
			}
		}
		res := a.resolveHelmReleasesFromPR(n)
		return &res
	}

	res := a.resolveHelmReleasesFromSHA(sha)
	return &res
}

func directFocusFromQuery(kind, namespace, name string) *FocusResolution {
	kind = normalizeFocusKind(kind)
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)

	values := url.Values{}
	if kind != "" {
		values.Set("kind", kind)
	}
	if namespace != "" {
		values.Set("ns", namespace)
	}
	if name != "" {
		values.Set("name", name)
	}

	res := &FocusResolution{
		RawParam: values.Encode(),
		Source:   fmt.Sprintf("%s %s/%s", defaultString(kind, "FluxObject"), defaultString(namespace, "default"), defaultString(name, "unknown")),
	}
	if namespace == "" || name == "" {
		res.Error = "direct object mode requires ns and name"
		return res
	}
	if kind == "" {
		res.Error = "invalid kind (use HelmRelease, Kustomization, or GitRepository)"
		return res
	}
	res.Targets = []FluxObjectRef{{Kind: kind, Namespace: namespace, Name: name}}
	return res
}

func normalizeFocusKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "helmrelease", "helmreleases":
		if strings.TrimSpace(raw) == "" {
			return "HelmRelease"
		}
		return "HelmRelease"
	case "kustomization", "kustomizations":
		return "Kustomization"
	case "gitrepository", "gitrepositories":
		return "GitRepository"
	default:
		return ""
	}
}

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	focus := a.focusFromRequest(r)
	a.touchWithFocus(focus)
	renderComponent(w, r, DashboardPage(a.dashboardViewModel(focus)))
}

func (a *App) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleUIStatus is the HTMX refresh endpoint for the focused status panel.
func (a *App) handleUIStatus(w http.ResponseWriter, r *http.Request) {
	focus := a.focusFromRequest(r)
	a.touchWithFocus(focus)
	renderComponent(w, r, StatusPanel(a.dashboardViewModel(focus)))
}

func (a *App) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}

	var evt FluxEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		log.Printf("webhook: invalid json body=%s error=%v", string(body), err)
		http.Error(w, "invalid json payload", http.StatusBadRequest)
		return
	}

	receivedAt := time.Now().UTC()
	record, err := a.store.InsertEvent(evt, receivedAt)
	if err != nil {
		log.Printf("webhook: failed to persist event: %v", err)
		http.Error(w, "failed to persist event", http.StatusInternalServerError)
		return
	}

	summary := evt.Summary()
	summary["received_at"] = record.ReceivedAt.Format(http.TimeFormat)
	summary["github_comment_mode"] = dispatchMode(a.github.Enabled && a.github.PRComment, a.github.Token != "" && a.github.Repo != "")
	summary["event_session_key"] = record.SessionKey
	pretty, _ := json.MarshalIndent(summary, "", "  ")
	log.Printf("flux event received:\n%s", string(pretty))

	go a.dispatch(evt)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"summary":   summary,
		"rawFields": sortedKeys(evt.Metadata),
	})
}
