package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type ConditionSnapshot struct {
	Status         string
	Reason         string
	Message        string
	LastTransition string
}

type FluxObjectRecord struct {
	APIGroup            string
	APIVersion          string
	Kind                string
	Namespace           string
	Name                string
	SourceKind          string
	SourceNamespace     string
	SourceName          string
	Revision            string
	LastAppliedRevision string
	LastAttemptedRev    string
	CommitSHA           string
	State               string
	Ready               ConditionSnapshot
	Reconciling         ConditionSnapshot
	Stalled             ConditionSnapshot
	ObservedGeneration  int64
	Generation          int64
	IntervalSeconds     int64
	UpdatedAt           time.Time
}

func (r FluxObjectRecord) ObjectKey() string {
	return fmt.Sprintf("%s/%s/%s", defaultString(r.Namespace, "default"), defaultString(r.Kind, "Unknown"), defaultString(r.Name, "unknown"))
}

type EventRecord struct {
	ID         int64
	SessionKey string
	ReceivedAt time.Time
	Event      FluxEvent
}

type Store struct {
	db *sql.DB
}

func NewStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) init() error {
	schema := []string{
		`PRAGMA journal_mode = WAL;`,
		`CREATE TABLE IF NOT EXISTS flux_objects (
			api_group TEXT NOT NULL,
			api_version TEXT NOT NULL,
			kind TEXT NOT NULL,
			namespace TEXT NOT NULL,
			name TEXT NOT NULL,
			source_kind TEXT NOT NULL DEFAULT '',
			source_namespace TEXT NOT NULL DEFAULT '',
			source_name TEXT NOT NULL DEFAULT '',
			revision TEXT NOT NULL DEFAULT '',
			last_applied_revision TEXT NOT NULL DEFAULT '',
			last_attempted_revision TEXT NOT NULL DEFAULT '',
			commit_sha TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT 'unknown',
			ready_status TEXT NOT NULL DEFAULT '',
			ready_reason TEXT NOT NULL DEFAULT '',
			ready_message TEXT NOT NULL DEFAULT '',
			ready_last_transition TEXT NOT NULL DEFAULT '',
			reconciling_status TEXT NOT NULL DEFAULT '',
			reconciling_reason TEXT NOT NULL DEFAULT '',
			reconciling_message TEXT NOT NULL DEFAULT '',
			reconciling_last_transition TEXT NOT NULL DEFAULT '',
			stalled_status TEXT NOT NULL DEFAULT '',
			stalled_reason TEXT NOT NULL DEFAULT '',
			stalled_message TEXT NOT NULL DEFAULT '',
			stalled_last_transition TEXT NOT NULL DEFAULT '',
			observed_generation INTEGER NOT NULL DEFAULT 0,
			generation INTEGER NOT NULL DEFAULT 0,
			interval_seconds INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (api_group, kind, namespace, name)
		);`,
		`CREATE TABLE IF NOT EXISTS flux_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_key TEXT NOT NULL DEFAULT '',
			api_version TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			severity TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			controller TEXT NOT NULL DEFAULT '',
			revision TEXT NOT NULL DEFAULT '',
			commit_sha TEXT NOT NULL DEFAULT '',
			timestamp TEXT NOT NULL DEFAULT '',
			received_at TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}'
		);`,
		`CREATE INDEX IF NOT EXISTS idx_flux_events_received_at ON flux_events(received_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_flux_events_session_key ON flux_events(session_key);`,
	}

	for _, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertFluxObject(record FluxObjectRecord) error {
	_, err := s.db.Exec(`INSERT INTO flux_objects (
		api_group, api_version, kind, namespace, name,
		source_kind, source_namespace, source_name,
		revision, last_applied_revision, last_attempted_revision, commit_sha, state,
		ready_status, ready_reason, ready_message, ready_last_transition,
		reconciling_status, reconciling_reason, reconciling_message, reconciling_last_transition,
		stalled_status, stalled_reason, stalled_message, stalled_last_transition,
		observed_generation, generation, interval_seconds, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(api_group, kind, namespace, name) DO UPDATE SET
		api_version=excluded.api_version,
		source_kind=excluded.source_kind,
		source_namespace=excluded.source_namespace,
		source_name=excluded.source_name,
		revision=excluded.revision,
		last_applied_revision=excluded.last_applied_revision,
		last_attempted_revision=excluded.last_attempted_revision,
		commit_sha=excluded.commit_sha,
		state=excluded.state,
		ready_status=excluded.ready_status,
		ready_reason=excluded.ready_reason,
		ready_message=excluded.ready_message,
		ready_last_transition=excluded.ready_last_transition,
		reconciling_status=excluded.reconciling_status,
		reconciling_reason=excluded.reconciling_reason,
		reconciling_message=excluded.reconciling_message,
		reconciling_last_transition=excluded.reconciling_last_transition,
		stalled_status=excluded.stalled_status,
		stalled_reason=excluded.stalled_reason,
		stalled_message=excluded.stalled_message,
		stalled_last_transition=excluded.stalled_last_transition,
		observed_generation=excluded.observed_generation,
		generation=excluded.generation,
		interval_seconds=excluded.interval_seconds,
		updated_at=excluded.updated_at;`,
		record.APIGroup, record.APIVersion, record.Kind, record.Namespace, record.Name,
		record.SourceKind, record.SourceNamespace, record.SourceName,
		record.Revision, record.LastAppliedRevision, record.LastAttemptedRev, record.CommitSHA, record.State,
		record.Ready.Status, record.Ready.Reason, record.Ready.Message, record.Ready.LastTransition,
		record.Reconciling.Status, record.Reconciling.Reason, record.Reconciling.Message, record.Reconciling.LastTransition,
		record.Stalled.Status, record.Stalled.Reason, record.Stalled.Message, record.Stalled.LastTransition,
		record.ObservedGeneration, record.Generation, record.IntervalSeconds, record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) DeleteFluxObject(apiGroup, kind, namespace, name string) error {
	_, err := s.db.Exec(`DELETE FROM flux_objects WHERE api_group = ? AND kind = ? AND namespace = ? AND name = ?`, apiGroup, kind, namespace, name)
	return err
}

func (s *Store) InsertEvent(evt FluxEvent, receivedAt time.Time) (EventRecord, error) {
	metadataJSON, err := json.Marshal(evt.Metadata)
	if err != nil {
		return EventRecord{}, err
	}

	revision := evt.Revision()
	commitSHA := evt.CommitSHA()
	sessionKey := eventSessionKey(evt)
	result, err := s.db.Exec(`INSERT INTO flux_events (
		session_key, api_version, kind, namespace, name,
		severity, reason, message, controller,
		revision, commit_sha, timestamp, received_at, metadata_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionKey,
		evt.InvolvedObject.APIVersion,
		evt.InvolvedObject.Kind,
		evt.InvolvedObject.Namespace,
		evt.InvolvedObject.Name,
		evt.Severity,
		evt.Reason,
		evt.Message,
		evt.ReportingController,
		revision,
		commitSHA,
		evt.Timestamp,
		receivedAt.UTC().Format(time.RFC3339Nano),
		string(metadataJSON),
	)
	if err != nil {
		return EventRecord{}, err
	}

	id, _ := result.LastInsertId()
	return EventRecord{ID: id, SessionKey: sessionKey, ReceivedAt: receivedAt, Event: evt}, nil
}

func (s *Store) ListObjects() ([]FluxObjectRecord, error) {
	rows, err := s.db.Query(`SELECT
		api_group, api_version, kind, namespace, name,
		source_kind, source_namespace, source_name,
		revision, last_applied_revision, last_attempted_revision, commit_sha, state,
		ready_status, ready_reason, ready_message, ready_last_transition,
		reconciling_status, reconciling_reason, reconciling_message, reconciling_last_transition,
		stalled_status, stalled_reason, stalled_message, stalled_last_transition,
		observed_generation, generation, interval_seconds, updated_at
	FROM flux_objects
	ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FluxObjectRecord
	for rows.Next() {
		var record FluxObjectRecord
		var updatedAt string
		if err := rows.Scan(
			&record.APIGroup, &record.APIVersion, &record.Kind, &record.Namespace, &record.Name,
			&record.SourceKind, &record.SourceNamespace, &record.SourceName,
			&record.Revision, &record.LastAppliedRevision, &record.LastAttemptedRev, &record.CommitSHA, &record.State,
			&record.Ready.Status, &record.Ready.Reason, &record.Ready.Message, &record.Ready.LastTransition,
			&record.Reconciling.Status, &record.Reconciling.Reason, &record.Reconciling.Message, &record.Reconciling.LastTransition,
			&record.Stalled.Status, &record.Stalled.Reason, &record.Stalled.Message, &record.Stalled.LastTransition,
			&record.ObservedGeneration, &record.Generation, &record.IntervalSeconds, &updatedAt,
		); err != nil {
			return nil, err
		}
		record.UpdatedAt = parseStoredTime(updatedAt)
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) ListRecentEvents(limit int) ([]EventRecord, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(`SELECT id, session_key, api_version, kind, namespace, name, severity, reason, message, controller, revision, commit_sha, timestamp, received_at, metadata_json
	FROM flux_events
	ORDER BY received_at DESC
	LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EventRecord
	for rows.Next() {
		var record EventRecord
		var revision string
		var commitSHA string
		var receivedAt string
		var metadataJSON string
		if err := rows.Scan(
			&record.ID,
			&record.SessionKey,
			&record.Event.InvolvedObject.APIVersion,
			&record.Event.InvolvedObject.Kind,
			&record.Event.InvolvedObject.Namespace,
			&record.Event.InvolvedObject.Name,
			&record.Event.Severity,
			&record.Event.Reason,
			&record.Event.Message,
			&record.Event.ReportingController,
			&revision,
			&commitSHA,
			&record.Event.Timestamp,
			&receivedAt,
			&metadataJSON,
		); err != nil {
			return nil, err
		}

		record.ReceivedAt = parseStoredTime(receivedAt)
		if metadataJSON != "" {
			_ = json.Unmarshal([]byte(metadataJSON), &record.Event.Metadata)
		}
		if record.Event.Metadata == nil {
			record.Event.Metadata = map[string]string{}
		}
		if revision != "" {
			record.Event.Metadata["revision"] = revision
		}
		if commitSHA != "" && record.Event.Metadata["commit_sha"] == "" {
			record.Event.Metadata["commit_sha"] = commitSHA
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func parseStoredTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, httpTimeLayout} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

const httpTimeLayout = "Mon, 02 Jan 2006 15:04:05 MST"
