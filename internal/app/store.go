package app

import (
	"fmt"
	"strings"
	"time"
)

type DashboardViewModel struct {
	GeneratedAtLabel string
	WatchMode        string
	WatchDetail      string
	Focus            *FocusViewState
	FocusQuery       string // appended to HTMX refresh URLs
	FocusPR          string // non-empty when focusing on a PR
	FocusSHA         string // non-empty when focusing on a commit SHA
	FocusKind        string // non-empty when focusing directly on a Flux object
	FocusNamespace   string
	FocusName        string
	Targets          []TargetViewModel   // focused Flux objects
	Events           []EventRowViewModel // events for focused objects
}

// FocusViewState is the template-facing view of a FocusResolution.
type FocusViewState struct {
	Source  string
	HeadSHA string
	Error   string
	Files   []string
	Targets []FluxObjectRef
}

type TargetViewModel struct {
	Kind       string
	Namespace  string
	Name       string
	StateLabel string
	StateClass string
	Revision   string
	CommitSHA  string
	Source     string
	Reason     string
	Message    string
	UpdatedAt  string
	Diagnostic *DiagnosticViewModel
}

type EventRowViewModel struct {
	ResourceLabel string
	Severity      string
	SeverityClass string
	Reason        string
	Controller    string
	Revision      string
	CommitSHA     string
	Message       string
	ShortMessage  string
	RelativeAge   string
	FluxTimeLabel string
}

type projectedObject struct {
	Record            FluxObjectRecord
	EffectiveRevision string
	EffectiveCommit   string
	Reason            string
	Message           string
	SourceLabel       string
}

func (a *App) dashboardViewModel(focus *FocusResolution) DashboardViewModel {
	watchMode, watchDetail := a.watchStatus.Snapshot()
	base := DashboardViewModel{
		GeneratedAtLabel: time.Now().Format("15:04:05"),
		WatchMode:        watchMode,
		WatchDetail:      watchDetail,
	}

	if focus != nil {
		base.Focus = &FocusViewState{
			Source:  focus.Source,
			HeadSHA: focus.HeadSHA,
			Error:   focus.Error,
			Files:   focus.Files,
			Targets: focus.Targets,
		}
		if focus.Source != "" {
			base.WatchDetail = focus.Source
		}
		base.FocusQuery = focus.RawParam
		if strings.HasPrefix(focus.RawParam, "pr=") {
			base.FocusPR = strings.TrimPrefix(focus.RawParam, "pr=")
		} else if strings.HasPrefix(focus.RawParam, "sha=") {
			base.FocusSHA = strings.TrimPrefix(focus.RawParam, "sha=")
		} else {
			for _, part := range strings.Split(focus.RawParam, "&") {
				kv := strings.SplitN(part, "=", 2)
				if len(kv) != 2 {
					continue
				}
				switch kv[0] {
				case "kind":
					base.FocusKind = kv[1]
				case "ns":
					base.FocusNamespace = kv[1]
				case "name":
					base.FocusName = kv[1]
				}
			}
		}
	}

	if focus == nil || len(focus.Targets) == 0 {
		return base
	}

	objects, err := a.store.ListObjects()
	if err != nil {
		base.WatchDetail = "store error: " + err.Error()
		return base
	}
	objects = filterObjectsByFocus(objects, focus.Targets)
	if len(objects) < len(focus.Targets) {
		if err := a.hydrateFocusedObjects(focus.Targets); err == nil {
			objects, err = a.store.ListObjects()
			if err != nil {
				base.WatchDetail = "store error: " + err.Error()
				return base
			}
			objects = filterObjectsByFocus(objects, focus.Targets)
		}
	}

	projected := projectObjects(objects)
	targets := make([]TargetViewModel, 0, len(projected))
	for _, obj := range projected {
		vm := targetViewModel(obj)
		if strings.EqualFold(vm.Kind, "HelmRelease") && vm.StateClass == "state-failed" {
			vm.Diagnostic = a.helmReleaseDiagnosticVM(obj.Record.Namespace, obj.Record.Name)
		}
		targets = append(targets, vm)
	}
	base.Targets = targets

	events, err := a.store.ListRecentEvents(100)
	if err == nil {
		events = filterEventsByFocus(events, focus.Targets)
		eventRows := make([]EventRowViewModel, 0, len(events))
		for i, evt := range events {
			if i >= 20 {
				break
			}
			eventRows = append(eventRows, eventRowViewModel(evt))
		}
		base.Events = eventRows
	}

	return base
}

func filterObjectsByFocus(objects []FluxObjectRecord, refs []FluxObjectRef) []FluxObjectRecord {
	set := make(map[string]bool, len(refs))
	for _, r := range refs {
		set[r.Kind+"/"+r.Namespace+"/"+r.Name] = true
	}
	out := objects[:0:0]
	for _, obj := range objects {
		if set[obj.Kind+"/"+obj.Namespace+"/"+obj.Name] {
			out = append(out, obj)
		}
	}
	return out
}

func filterEventsByFocus(events []EventRecord, refs []FluxObjectRef) []EventRecord {
	set := make(map[string]bool, len(refs))
	for _, r := range refs {
		set[r.Kind+"/"+r.Namespace+"/"+r.Name] = true
	}
	out := events[:0:0]
	for _, evt := range events {
		key := defaultString(evt.Event.InvolvedObject.Kind, "Unknown") + "/" + defaultString(evt.Event.InvolvedObject.Namespace, "default") + "/" + evt.Event.InvolvedObject.Name
		if set[key] {
			out = append(out, evt)
		}
	}
	return out
}

func projectObjects(objects []FluxObjectRecord) []projectedObject {
	sourceIndex := map[string]FluxObjectRecord{}
	for _, obj := range objects {
		sourceIndex[obj.ObjectKey()] = obj
	}

	out := make([]projectedObject, 0, len(objects))
	for _, obj := range objects {
		effectiveRevision := firstNonEmpty(obj.Revision, obj.LastAppliedRevision, obj.LastAttemptedRev)
		effectiveCommit := firstNonEmpty(obj.CommitSHA, commitSHAFromRevision(effectiveRevision))
		sourceLabel := "direct"

		if obj.SourceName != "" {
			sourceNamespace := obj.SourceNamespace
			if sourceNamespace == "" {
				sourceNamespace = obj.Namespace
			}
			sourceLabel = fmt.Sprintf("%s/%s/%s", sourceNamespace, defaultString(obj.SourceKind, "Source"), obj.SourceName)
			sourceKey := fmt.Sprintf("%s/%s/%s", sourceNamespace, defaultString(obj.SourceKind, "Unknown"), obj.SourceName)
			if source, ok := sourceIndex[sourceKey]; ok {
				effectiveRevision = firstNonEmpty(effectiveRevision, source.Revision, source.LastAppliedRevision, source.LastAttemptedRev)
				effectiveCommit = firstNonEmpty(effectiveCommit, source.CommitSHA, commitSHAFromRevision(source.Revision))
			}
		}

		reason, message := currentIssue(obj)
		out = append(out, projectedObject{
			Record:            obj,
			EffectiveRevision: effectiveRevision,
			EffectiveCommit:   effectiveCommit,
			Reason:            reason,
			Message:           message,
			SourceLabel:       sourceLabel,
		})
	}

	return out
}

func targetViewModel(target projectedObject) TargetViewModel {
	revision := defaultString(target.EffectiveRevision, "n/a")
	commitSHA := defaultString(shortSHA(target.EffectiveCommit), "n/a")
	reason := defaultString(target.Reason, "")
	message := defaultString(target.Message, "")
	return TargetViewModel{
		Kind:       defaultString(target.Record.Kind, "Unknown"),
		Namespace:  defaultString(target.Record.Namespace, "default"),
		Name:       defaultString(target.Record.Name, "unknown"),
		StateLabel: strings.ToUpper(target.Record.State),
		StateClass: stateClass(target.Record.State),
		Revision:   revision,
		CommitSHA:  commitSHA,
		Source:     target.SourceLabel,
		Reason:     reason,
		Message:    message,
		UpdatedAt:  relativeTime(target.Record.UpdatedAt),
	}
}

func eventRowViewModel(record EventRecord) EventRowViewModel {
	evt := record.Event
	return EventRowViewModel{
		ResourceLabel: fmt.Sprintf("%s / %s / %s", defaultString(evt.InvolvedObject.Namespace, "default"), defaultString(evt.InvolvedObject.Kind, "Unknown"), defaultString(evt.InvolvedObject.Name, "unknown")),
		Severity:      strings.ToUpper(defaultString(evt.Severity, "unknown")),
		SeverityClass: eventSeverityClass(evt.Severity),
		Reason:        defaultString(evt.Reason, "n/a"),
		Controller:    defaultString(evt.ReportingController, "n/a"),
		Revision:      defaultString(evt.Revision(), "n/a"),
		CommitSHA:     defaultString(shortSHA(evt.CommitSHA()), "n/a"),
		Message:       evt.Message,
		ShortMessage:  truncate(singleLine(evt.Message), 220),
		RelativeAge:   relativeTime(record.ReceivedAt),
		FluxTimeLabel: formatFluxTimestamp(evt.Timestamp),
	}
}

func currentIssue(record FluxObjectRecord) (string, string) {
	for _, condition := range []ConditionSnapshot{record.Ready, record.Stalled, record.Reconciling} {
		if condition.Reason != "" || condition.Message != "" {
			return condition.Reason, condition.Message
		}
	}
	return "", ""
}

func commitSHAFromRevision(revision string) string {
	evt := FluxEvent{Metadata: map[string]string{"revision": revision}}
	return evt.CommitSHA()
}

func stateClass(state string) string {
	switch state {
	case "ready":
		return "state-ready"
	case "reconciling":
		return "state-reconciling"
	case "failed", "stalled":
		return "state-failed"
	default:
		return "state-unknown"
	}
}

func eventSeverityClass(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "error":
		return "sev-error"
	case "info":
		return "sev-info"
	case "warning":
		return "sev-warn"
	default:
		return "sev-muted"
	}
}

func formatFluxTimestamp(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "n/a"
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.Local().Format("02 Jan 15:04:05")
		}
	}
	return raw
}

// eventSessionKey is used by the store when persisting events so they can be
// correlated with watched objects later.
func eventSessionKey(evt FluxEvent) string {
	if sha := evt.CommitSHA(); sha != "" {
		return "sha:" + sha
	}
	if revision := evt.Revision(); revision != "" {
		return "revision:" + revision
	}
	return fmt.Sprintf("object:%s/%s/%s",
		defaultString(evt.InvolvedObject.Namespace, "default"),
		defaultString(evt.InvolvedObject.Kind, "Unknown"),
		defaultString(evt.InvolvedObject.Name, "unknown"),
	)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
