package app

import (
	"fmt"
	"regexp"
	"strings"
)

type FluxEvent struct {
	InvolvedObject      InvolvedObject    `json:"involvedObject"`
	Metadata            map[string]string `json:"metadata,omitempty"`
	Severity            string            `json:"severity"`
	Reason              string            `json:"reason"`
	Message             string            `json:"message"`
	ReportingController string            `json:"reportingController"`
	Timestamp           string            `json:"timestamp"`
}

type InvolvedObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	UID        string `json:"uid"`
}

func (e FluxEvent) Revision() string {
	if len(e.Metadata) == 0 {
		return ""
	}

	for _, key := range []string{
		"kustomize.toolkit.fluxcd.io/revision",
		"helm.toolkit.fluxcd.io/revision",
		"source.toolkit.fluxcd.io/revision",
		"revision",
	} {
		if v := e.Metadata[key]; v != "" {
			return v
		}
	}

	for _, key := range sortedKeys(e.Metadata) {
		if strings.Contains(strings.ToLower(key), "revision") {
			return e.Metadata[key]
		}
	}

	return ""
}

var shaPattern = regexp.MustCompile(`^[a-fA-F0-9]{7,40}$`)

func (e FluxEvent) CommitSHA() string {
	rev := strings.TrimSpace(e.Revision())
	if rev == "" {
		return ""
	}

	candidates := []string{rev}
	if idx := strings.LastIndex(rev, "/"); idx >= 0 && idx+1 < len(rev) {
		candidates = append(candidates, rev[idx+1:])
	}
	if idx := strings.LastIndex(rev, "sha1:"); idx >= 0 && idx+5 < len(rev) {
		candidates = append(candidates, rev[idx+5:])
	}
	if idx := strings.LastIndex(rev, ":"); idx >= 0 && idx+1 < len(rev) {
		candidates = append(candidates, rev[idx+1:])
	}

	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if shaPattern.MatchString(candidate) {
			return candidate
		}
	}
	return ""
}

func (e FluxEvent) Summary() map[string]any {
	return map[string]any{
		"kind":                e.InvolvedObject.Kind,
		"name":                e.InvolvedObject.Name,
		"namespace":           e.InvolvedObject.Namespace,
		"severity":            e.Severity,
		"reason":              e.Reason,
		"message":             e.Message,
		"reportingController": e.ReportingController,
		"timestamp":           e.Timestamp,
		"revision":            e.Revision(),
		"commit_sha":          e.CommitSHA(),
		"metadata":            e.Metadata,
	}
}

func (e FluxEvent) String() string {
	return fmt.Sprintf("%s/%s kind=%s severity=%s reason=%s revision=%s", e.InvolvedObject.Namespace, e.InvolvedObject.Name, e.InvolvedObject.Kind, e.Severity, e.Reason, e.Revision())
}
