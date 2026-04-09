package main

import (
	"context"
	"log"
	"sync"
	"time"
)

type WatchController struct {
	mu         sync.Mutex
	running    bool
	ctx        context.Context
	cancel     context.CancelFunc
	lastActive time.Time
	timeout    time.Duration
	status     *WatchStatus
	run        func(context.Context) error
	generation int64
}

func NewWatchController(timeout time.Duration, status *WatchStatus, run func(context.Context) error) *WatchController {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	wc := &WatchController{
		timeout: timeout,
		status:  status,
		run:     run,
	}
	wc.status.Set("idle", "watches start only when the UI is active")
	go wc.reapLoop()
	return wc
}

func (wc *WatchController) Touch() {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	wc.lastActive = time.Now()
	if wc.running {
		return
	}
	wc.startLocked()
}

func (wc *WatchController) startLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	wc.ctx = ctx
	wc.cancel = cancel
	wc.running = true
	wc.generation++
	generation := wc.generation
	wc.status.Set("connecting", "starting shared Flux watches")

	go func() {
		backoff := time.Second
		for {
			err := wc.run(ctx)
			if ctx.Err() != nil {
				wc.mu.Lock()
				defer wc.mu.Unlock()
				if generation != wc.generation {
					return
				}
				wc.running = false
				wc.ctx = nil
				wc.cancel = nil
				wc.status.Set("idle", "watches paused after UI inactivity")
				return
			}
			if err == nil {
				wc.mu.Lock()
				defer wc.mu.Unlock()
				if generation != wc.generation {
					return
				}
				wc.running = false
				wc.ctx = nil
				wc.cancel = nil
				wc.status.Set("idle", "watches stopped")
				return
			}

			wc.status.Set("degraded", "watch error, retrying: "+err.Error())
			log.Printf("watch controller error: %v", err)
			select {
			case <-ctx.Done():
				wc.mu.Lock()
				defer wc.mu.Unlock()
				if generation != wc.generation {
					return
				}
				wc.running = false
				wc.ctx = nil
				wc.cancel = nil
				wc.status.Set("idle", "watches paused after UI inactivity")
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
			wc.status.Set("connecting", "retrying shared Flux watches")
		}
	}()
}

func (wc *WatchController) reapLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		wc.mu.Lock()
		if wc.running && !wc.lastActive.IsZero() && time.Since(wc.lastActive) > wc.timeout {
			cancel := wc.cancel
			wc.cancel = nil
			wc.ctx = nil
			wc.running = false
			wc.generation++
			wc.status.Set("idle", "watches paused after UI inactivity")
			wc.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			continue
		}
		wc.mu.Unlock()
	}
}
