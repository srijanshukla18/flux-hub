package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

func (a *App) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	a.touchUI()
	renderComponent(w, r, DashboardPage(a.dashboardViewModel()))
}

func (a *App) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (a *App) handleUISummary(w http.ResponseWriter, r *http.Request) {
	a.touchUI()
	renderComponent(w, r, DashboardSummary(a.dashboardViewModel()))
}

func (a *App) handleUISessions(w http.ResponseWriter, r *http.Request) {
	a.touchUI()
	renderComponent(w, r, DashboardSessions(a.dashboardViewModel()))
}

func (a *App) handleUIEvents(w http.ResponseWriter, r *http.Request) {
	a.touchUI()
	renderComponent(w, r, DashboardEvents(a.dashboardViewModel().Events))
}

func (a *App) handleSessionPage(w http.ResponseWriter, r *http.Request) {
	a.touchUI()
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing session key", http.StatusBadRequest)
		return
	}

	vm, ok := a.sessionPageViewModel(key)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	renderComponent(w, r, SessionPage(vm))
}

func (a *App) handleUISessionBody(w http.ResponseWriter, r *http.Request) {
	a.touchUI()
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing session key", http.StatusBadRequest)
		return
	}

	vm, ok := a.sessionPageViewModel(key)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	renderComponent(w, r, SessionBodyRoot(vm))
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
	summary["github_dispatch_mode"] = dispatchMode(a.github.Enabled, a.github.Token != "" && a.github.Repo != "")
	summary["slack_dispatch_mode"] = dispatchMode(a.slack.Enabled, a.slack.WebhookURL != "")
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
