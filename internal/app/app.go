package app

import (
	"net/http"
	"sync"
	"time"
)

// FluxObjectRef identifies a specific Flux object by kind, namespace and name.
type FluxObjectRef struct {
	Kind      string
	Namespace string
	Name      string
}

// FocusResolution is the result of resolving a PR number, commit SHA, or
// direct object query into a set of Flux objects to watch.
type FocusResolution struct {
	RawParam string // e.g. "pr=123", "sha=abc123", or "kind=HelmRelease&ns=default&name=podinfo"
	Source   string // display label e.g. "PR #123", "commit abc1234", or "HelmRelease default/podinfo"
	HeadSHA  string // resolved commit SHA (for PRs, the head commit)
	Files    []string
	Targets  []FluxObjectRef
	Error    string
}

type focusCacheEntry struct {
	key        string
	resolution FocusResolution
}

type App struct {
	listenAddr  string
	httpClient  *http.Client
	github      GitHubConfig
	slack       SlackConfig
	store       *Store
	startedAt   time.Time
	watcher     *WatchController
	watchStatus *WatchStatus

	focusMu    sync.Mutex
	focusCache *focusCacheEntry // simple single-entry cache keyed by RawParam
}

type WatchStatus struct {
	mu     sync.RWMutex
	mode   string
	detail string
}

func NewApp() (*App, error) {
	store, err := NewStore(envOrDefault("DATABASE_PATH", "/tmp/flux-hub.db"))
	if err != nil {
		return nil, err
	}

	app := &App{
		listenAddr:  envOrDefault("LISTEN_ADDR", ":8080"),
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		github:      loadGitHubConfig(),
		slack:       loadSlackConfig(),
		store:       store,
		startedAt:   time.Now(),
		watchStatus: &WatchStatus{},
	}
	app.watcher = NewWatchController(durationEnvOrDefault("WATCH_IDLE_TIMEOUT", 2*time.Minute), app.watchStatus, app.runFluxWatches)
	return app, nil
}

func (a *App) Close() error {
	if a.store != nil {
		return a.store.Close()
	}
	return nil
}

// cachedFocusResolution returns the cached result for key, if any.
func (a *App) cachedFocusResolution(key string) (FocusResolution, bool) {
	a.focusMu.Lock()
	defer a.focusMu.Unlock()
	if a.focusCache != nil && a.focusCache.key == key {
		return a.focusCache.resolution, true
	}
	return FocusResolution{}, false
}

func (a *App) setCachedFocusResolution(key string, r FocusResolution) {
	a.focusMu.Lock()
	defer a.focusMu.Unlock()
	a.focusCache = &focusCacheEntry{key: key, resolution: r}
}

// touchWithFocus keeps the Kubernetes watch alive for the given focus.
// Uses focus.RawParam as the per-client key so multiple concurrent focused
// views each maintain their own entry in the union without fighting.
func (a *App) touchWithFocus(focus *FocusResolution) {
	if focus != nil && len(focus.Targets) > 0 {
		a.watcher.TouchWithFocus(focus.RawParam, focus.Targets)
	}
}

func (w *WatchStatus) Set(mode, detail string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.mode = mode
	w.detail = detail
}

func (w *WatchStatus) Snapshot() (string, string) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.mode, w.detail
}
