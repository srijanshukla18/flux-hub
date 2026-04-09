package main

import (
	"net/http"
	"sync"
	"time"
)

type App struct {
	listenAddr  string
	httpClient  *http.Client
	github      GitHubConfig
	slack       SlackConfig
	store       *Store
	startedAt   time.Time
	watcher     *WatchController
	watchStatus *WatchStatus
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

func (a *App) touchUI() {
	if a.watcher != nil {
		a.watcher.Touch()
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

func (w *WatchStatus) Mode() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.mode
}
