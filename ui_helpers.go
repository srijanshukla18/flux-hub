package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/a-h/templ"
)

func renderComponent(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		http.Error(w, fmt.Sprintf("failed to render page: %v", err), http.StatusInternalServerError)
	}
}

func integrationTone(mode string) string {
	switch mode {
	case "enabled", "connected", "ready":
		return "tone-success"
	case "dry-run", "reconciling", "connecting":
		return "tone-orange"
	case "degraded", "failed", "stalled":
		return "tone-error"
	default:
		return "tone-muted"
	}
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func shortSHA(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = -d
	}

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
