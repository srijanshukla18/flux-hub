package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type DashboardViewModel struct {
	GeneratedAtLabel  string
	UptimeLabel       string
	GitHubMode        string
	GitHubCommentMode string
	SlackMode         string
	WatchMode         string
	WatchDetail       string
	TrackedObjects    int
	FailedObjects     int
	RecentEvents      int
	DeploymentThreads int
	Sessions          []SessionRowViewModel
	Events            []EventRowViewModel
}

type SessionRowViewModel struct {
	DetailHref       string
	Title            string
	Subtitle         string
	StateLabel       string
	StateClass       string
	Revision         string
	CommitSHA        string
	ResourceCount    int
	ReadyCount       int
	ReconcilingCount int
	FailedCount      int
	StalledCount     int
	EventCount       int
	LastSeenLabel    string
	Blocker          string
}

type SessionPageViewModel struct {
	RefreshHref      string
	StateKey         string
	IsTerminal       bool
	Title            string
	Subtitle         string
	StateLabel       string
	StateClass       string
	Revision         string
	CommitSHA        string
	ResourceCount    int
	ReadyCount       int
	ReconcilingCount int
	FailedCount      int
	StalledCount     int
	EventCount       int
	LastSeenLabel    string
	Blocker          string
	Targets          []TargetViewModel
	Events           []EventRowViewModel
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
	SessionKey        string
	Reason            string
	Message           string
	SourceLabel       string
}

type sessionAggregate struct {
	Key       string
	Revision  string
	CommitSHA string
	Targets   []projectedObject
	Events    []EventRecord
	LastSeen  time.Time
}

func (a *App) dashboardViewModel() DashboardViewModel {
	objects, err := a.store.ListObjects()
	if err != nil {
		return DashboardViewModel{
			GeneratedAtLabel:  time.Now().Format("15:04:05 MST"),
			UptimeLabel:       relativeTime(a.startedAt),
			GitHubMode:        dispatchMode(a.github.Enabled, a.github.Token != "" && a.github.Repo != ""),
			GitHubCommentMode: dispatchMode(a.github.Enabled && a.github.PRComment, a.github.Token != "" && a.github.Repo != ""),
			SlackMode:         dispatchMode(a.slack.Enabled, a.slack.WebhookURL != ""),
			WatchMode:         a.watchStatus.Mode(),
			WatchDetail:       "failed to read SQLite store: " + err.Error(),
		}
	}
	events, err := a.store.ListRecentEvents(200)
	if err != nil {
		events = nil
	}

	projected := projectObjects(objects)
	sessions := buildSessions(projected, events)

	failedObjects := 0
	for _, obj := range projected {
		if obj.Record.State == "failed" || obj.Record.State == "stalled" {
			failedObjects++
		}
	}

	rows := make([]SessionRowViewModel, 0, len(sessions))
	for i, session := range sessions {
		if i >= 12 {
			break
		}
		rows = append(rows, sessionRowViewModel(session))
	}

	eventRows := make([]EventRowViewModel, 0, len(events))
	for i, evt := range events {
		if i >= 20 {
			break
		}
		eventRows = append(eventRows, eventRowViewModel(evt))
	}

	watchMode, watchDetail := a.watchStatus.Snapshot()
	return DashboardViewModel{
		GeneratedAtLabel:  time.Now().Format("15:04:05 MST"),
		UptimeLabel:       relativeTime(a.startedAt),
		GitHubMode:        dispatchMode(a.github.Enabled, a.github.Token != "" && a.github.Repo != ""),
		GitHubCommentMode: dispatchMode(a.github.Enabled && a.github.PRComment, a.github.Token != "" && a.github.Repo != ""),
		SlackMode:         dispatchMode(a.slack.Enabled, a.slack.WebhookURL != ""),
		WatchMode:         watchMode,
		WatchDetail:       watchDetail,
		TrackedObjects:    len(projected),
		FailedObjects:     failedObjects,
		RecentEvents:      len(events),
		DeploymentThreads: len(sessions),
		Sessions:          rows,
		Events:            eventRows,
	}
}

func (a *App) sessionPageViewModel(key string) (SessionPageViewModel, bool) {
	objects, err := a.store.ListObjects()
	if err != nil {
		return SessionPageViewModel{}, false
	}
	events, err := a.store.ListRecentEvents(300)
	if err != nil {
		events = nil
	}

	sessions := buildSessions(projectObjects(objects), events)
	for _, session := range sessions {
		if session.Key != key {
			continue
		}
		row := sessionRowViewModel(session)
		targets := make([]TargetViewModel, 0, len(session.Targets))
		for _, target := range session.Targets {
			targets = append(targets, targetViewModel(target))
		}
		eventRows := make([]EventRowViewModel, 0, len(session.Events))
		for _, evt := range session.Events {
			eventRows = append(eventRows, eventRowViewModel(evt))
		}
		return SessionPageViewModel{
			RefreshHref:      "/ui/session-body?key=" + url.QueryEscape(session.Key),
			StateKey:         strings.ToLower(row.StateLabel),
			IsTerminal:       isTerminalState(strings.ToLower(row.StateLabel)),
			Title:            row.Title,
			Subtitle:         row.Subtitle,
			StateLabel:       row.StateLabel,
			StateClass:       row.StateClass,
			Revision:         row.Revision,
			CommitSHA:        row.CommitSHA,
			ResourceCount:    row.ResourceCount,
			ReadyCount:       row.ReadyCount,
			ReconcilingCount: row.ReconcilingCount,
			FailedCount:      row.FailedCount,
			StalledCount:     row.StalledCount,
			EventCount:       row.EventCount,
			LastSeenLabel:    row.LastSeenLabel,
			Blocker:          row.Blocker,
			Targets:          targets,
			Events:           eventRows,
		}, true
	}
	return SessionPageViewModel{}, false
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

		sessionKey := objectSessionKey(obj, effectiveRevision, effectiveCommit)
		reason, message := currentIssue(obj)
		out = append(out, projectedObject{
			Record:            obj,
			EffectiveRevision: effectiveRevision,
			EffectiveCommit:   effectiveCommit,
			SessionKey:        sessionKey,
			Reason:            reason,
			Message:           message,
			SourceLabel:       sourceLabel,
		})
	}

	return out
}

func buildSessions(objects []projectedObject, events []EventRecord) []*sessionAggregate {
	byKey := map[string]*sessionAggregate{}
	order := make([]string, 0)
	resourceToSession := map[string]string{}

	ensure := func(key string) *sessionAggregate {
		session, ok := byKey[key]
		if ok {
			return session
		}
		session = &sessionAggregate{Key: key}
		byKey[key] = session
		order = append(order, key)
		return session
	}

	for _, obj := range objects {
		session := ensure(obj.SessionKey)
		session.Targets = append(session.Targets, obj)
		session.Revision = firstNonEmpty(session.Revision, obj.EffectiveRevision)
		session.CommitSHA = firstNonEmpty(session.CommitSHA, obj.EffectiveCommit)
		session.LastSeen = maxTime(session.LastSeen, obj.Record.UpdatedAt)
		resourceToSession[obj.Record.ObjectKey()] = obj.SessionKey
	}

	for _, evt := range events {
		key := evt.SessionKey
		resourceKey := fmt.Sprintf("%s/%s/%s", defaultString(evt.Event.InvolvedObject.Namespace, "default"), defaultString(evt.Event.InvolvedObject.Kind, "Unknown"), defaultString(evt.Event.InvolvedObject.Name, "unknown"))
		if mapped := resourceToSession[resourceKey]; mapped != "" {
			key = mapped
		}
		session := ensure(key)
		session.Events = append(session.Events, evt)
		session.Revision = firstNonEmpty(session.Revision, evt.Event.Revision())
		session.CommitSHA = firstNonEmpty(session.CommitSHA, evt.Event.CommitSHA(), commitSHAFromRevision(evt.Event.Revision()))
		session.LastSeen = maxTime(session.LastSeen, evt.ReceivedAt)
	}

	out := make([]*sessionAggregate, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	for _, session := range out {
		sort.Slice(session.Targets, func(i, j int) bool {
			return stateRank(session.Targets[i].Record.State) > stateRank(session.Targets[j].Record.State)
		})
		sort.Slice(session.Events, func(i, j int) bool {
			return session.Events[i].ReceivedAt.After(session.Events[j].ReceivedAt)
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out
}

func sessionRowViewModel(session *sessionAggregate) SessionRowViewModel {
	state, stateClass := sessionState(session)
	title, subtitle := sessionTitle(session)
	revision := defaultString(session.Revision, "n/a")
	commitSHA := defaultString(shortSHA(session.CommitSHA), "n/a")
	if commitSHA == "" {
		commitSHA = "n/a"
	}

	readyCount, reconcilingCount, failedCount, stalledCount := sessionCounts(session)
	blocker := sessionBlocker(session)
	if blocker == "" {
		blocker = "No active blocker recorded."
	}

	return SessionRowViewModel{
		DetailHref:       "/session?key=" + url.QueryEscape(session.Key),
		Title:            title,
		Subtitle:         subtitle,
		StateLabel:       strings.ToUpper(state),
		StateClass:       stateClass,
		Revision:         revision,
		CommitSHA:        commitSHA,
		ResourceCount:    len(session.Targets),
		ReadyCount:       readyCount,
		ReconcilingCount: reconcilingCount,
		FailedCount:      failedCount,
		StalledCount:     stalledCount,
		EventCount:       len(session.Events),
		LastSeenLabel:    relativeTime(session.LastSeen),
		Blocker:          blocker,
	}
}

func targetViewModel(target projectedObject) TargetViewModel {
	revision := defaultString(target.EffectiveRevision, "n/a")
	commitSHA := defaultString(shortSHA(target.EffectiveCommit), "n/a")
	if commitSHA == "" {
		commitSHA = "n/a"
	}
	reason := defaultString(target.Reason, "No current condition reason")
	message := defaultString(target.Message, "No current condition message")
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

func sessionState(session *sessionAggregate) (string, string) {
	worst := "unknown"
	for _, target := range session.Targets {
		if stateRank(target.Record.State) > stateRank(worst) {
			worst = target.Record.State
		}
	}
	if worst == "unknown" && len(session.Events) > 0 {
		if strings.EqualFold(session.Events[0].Event.Severity, "error") {
			worst = "failed"
		} else {
			worst = "observed"
		}
	}
	return worst, stateClass(worst)
}

func sessionCounts(session *sessionAggregate) (ready, reconciling, failed, stalled int) {
	for _, target := range session.Targets {
		switch target.Record.State {
		case "ready":
			ready++
		case "reconciling":
			reconciling++
		case "failed":
			failed++
		case "stalled":
			stalled++
		}
	}
	return
}

func sessionBlocker(session *sessionAggregate) string {
	for _, target := range session.Targets {
		if target.Record.State == "failed" || target.Record.State == "stalled" || target.Record.State == "reconciling" {
			if target.Reason != "" && target.Message != "" {
				return fmt.Sprintf("%s/%s: %s — %s", target.Record.Kind, target.Record.Name, target.Reason, truncate(singleLine(target.Message), 120))
			}
			if target.Message != "" {
				return fmt.Sprintf("%s/%s: %s", target.Record.Kind, target.Record.Name, truncate(singleLine(target.Message), 120))
			}
		}
	}
	if len(session.Events) > 0 {
		return truncate(singleLine(session.Events[0].Event.Message), 120)
	}
	return ""
}

func sessionTitle(session *sessionAggregate) (string, string) {
	if session.CommitSHA != "" {
		return "Commit " + shortSHA(session.CommitSHA), defaultString(session.Revision, "Flux deployment thread")
	}
	if session.Revision != "" {
		return session.Revision, fmt.Sprintf("%d Flux targets", len(session.Targets))
	}
	if len(session.Targets) > 0 {
		t := session.Targets[0].Record
		return fmt.Sprintf("%s/%s", t.Kind, t.Name), defaultString(t.Namespace, "default")
	}
	if len(session.Events) > 0 {
		e := session.Events[0].Event
		return fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name), defaultString(e.InvolvedObject.Namespace, "default")
	}
	return "Unknown session", "No linked Flux objects"
}

func currentIssue(record FluxObjectRecord) (string, string) {
	for _, condition := range []ConditionSnapshot{record.Stalled, record.Reconciling, record.Ready} {
		if condition.Reason != "" || condition.Message != "" {
			return condition.Reason, condition.Message
		}
	}
	return "", ""
}

func objectSessionKey(record FluxObjectRecord, revision, commitSHA string) string {
	if commitSHA != "" {
		return "sha:" + commitSHA
	}
	if revision != "" {
		return "revision:" + revision
	}
	if record.SourceName != "" {
		sourceNamespace := record.SourceNamespace
		if sourceNamespace == "" {
			sourceNamespace = record.Namespace
		}
		return fmt.Sprintf("source:%s/%s/%s", sourceNamespace, defaultString(record.SourceKind, "Source"), record.SourceName)
	}
	return "object:" + record.ObjectKey()
}

func eventSessionKey(evt FluxEvent) string {
	if sha := evt.CommitSHA(); sha != "" {
		return "sha:" + sha
	}
	if revision := evt.Revision(); revision != "" {
		return "revision:" + revision
	}
	return fmt.Sprintf("object:%s/%s/%s", defaultString(evt.InvolvedObject.Namespace, "default"), defaultString(evt.InvolvedObject.Kind, "Unknown"), defaultString(evt.InvolvedObject.Name, "unknown"))
}

func commitSHAFromRevision(revision string) string {
	evt := FluxEvent{Metadata: map[string]string{"revision": revision}}
	return evt.CommitSHA()
}

func stateRank(state string) int {
	switch state {
	case "stalled":
		return 5
	case "failed":
		return 4
	case "reconciling":
		return 3
	case "ready":
		return 2
	case "observed":
		return 1
	default:
		return 0
	}
}

func stateClass(state string) string {
	switch state {
	case "ready":
		return "tone-success"
	case "reconciling":
		return "tone-orange"
	case "failed", "stalled":
		return "tone-error"
	default:
		return "tone-muted"
	}
}

func isTerminalState(state string) bool {
	switch state {
	case "ready", "failed", "stalled":
		return true
	default:
		return false
	}
}

func eventSeverityClass(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "error":
		return "tone-error"
	case "info":
		return "tone-success"
	case "warning":
		return "tone-orange"
	default:
		return "tone-muted"
	}
}

func formatFluxTimestamp(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "n/a"
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.Local().Format("02 Jan 2006 15:04:05 MST")
		}
	}
	return raw
}

func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
