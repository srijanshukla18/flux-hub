package main

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

type focusEntry struct {
	targets  []FluxObjectRef
	lastSeen time.Time
}

type WatchController struct {
	mu            sync.Mutex
	running       bool
	ctx           context.Context
	cancel        context.CancelFunc
	timeout       time.Duration
	status        *WatchStatus
	run           func(context.Context) error
	generation    int64
	activeFocuses map[string]focusEntry // keyed by RawParam ("pr=123", "sha=abc", ...)
	watchedUnion  []FluxObjectRef       // union of all active focuses, sorted
}

func NewWatchController(timeout time.Duration, status *WatchStatus, run func(context.Context) error) *WatchController {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	wc := &WatchController{
		timeout:       timeout,
		status:        status,
		run:           run,
		activeFocuses: make(map[string]focusEntry),
	}
	wc.status.Set("idle", "no Flux objects targeted")
	go wc.reapLoop()
	return wc
}

// TouchWithFocus records UI activity for the given focus key (RawParam) and
// its targets. If the union across all active focuses changes, the running
// watch is cancelled and restarted with the new union target set.
func (wc *WatchController) TouchWithFocus(key string, targets []FluxObjectRef) {
	wc.mu.Lock()
	defer wc.mu.Unlock()

	wc.activeFocuses[key] = focusEntry{targets: targets, lastSeen: time.Now()}
	newUnion := wc.computeUnionLocked()

	if !fluxObjectRefsEqual(wc.watchedUnion, newUnion) {
		wc.watchedUnion = newUnion
		if wc.running && wc.cancel != nil {
			oldCancel := wc.cancel
			wc.cancel = nil
			wc.ctx = nil
			wc.running = false
			oldCancel() // old goroutine exits via ctx.Done(); startLocked increments generation
		}
	}

	if !wc.running && len(wc.watchedUnion) > 0 {
		wc.startLocked()
	}
}

// GetUnionFocus returns the current union of all active focus sets.
// runFluxWatches calls this at start time to know what to watch.
func (wc *WatchController) GetUnionFocus() []FluxObjectRef {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	return wc.watchedUnion
}

// computeUnionLocked deduplicates and sorts the union of all active focus sets.
// Must be called with mu held. Sorted so fluxObjectRefsEqual is stable.
func (wc *WatchController) computeUnionLocked() []FluxObjectRef {
	seen := map[string]bool{}
	var union []FluxObjectRef
	for _, entry := range wc.activeFocuses {
		for _, r := range entry.targets {
			k := r.Kind + "/" + r.Namespace + "/" + r.Name
			if !seen[k] {
				seen[k] = true
				union = append(union, r)
			}
		}
	}
	sort.Slice(union, func(i, j int) bool {
		ki := union[i].Kind + "/" + union[i].Namespace + "/" + union[i].Name
		kj := union[j].Kind + "/" + union[j].Namespace + "/" + union[j].Name
		return ki < kj
	})
	return union
}

func fluxObjectRefsEqual(a, b []FluxObjectRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (wc *WatchController) startLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	wc.ctx = ctx
	wc.cancel = cancel
	wc.running = true
	wc.generation++
	generation := wc.generation
	wc.status.Set("connecting", "starting Flux watches")

	go func() {
		backoff := time.Second
		for {
			err := wc.run(ctx)
			if ctx.Err() != nil {
				wc.mu.Lock()
				defer wc.mu.Unlock()
				if generation != wc.generation {
					return // superseded by a newer watch
				}
				wc.running = false
				wc.ctx = nil
				wc.cancel = nil
				wc.status.Set("idle", "watches stopped")
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
				wc.status.Set("idle", "watches stopped")
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
			wc.status.Set("connecting", "retrying Flux watches")
		}
	}()
}

func (wc *WatchController) reapLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		wc.mu.Lock()
		now := time.Now()
		evicted := false
		for key, entry := range wc.activeFocuses {
			if now.Sub(entry.lastSeen) > wc.timeout {
				delete(wc.activeFocuses, key)
				evicted = true
			}
		}
		if evicted {
			newUnion := wc.computeUnionLocked()
			if !fluxObjectRefsEqual(wc.watchedUnion, newUnion) {
				wc.watchedUnion = newUnion
				if wc.running && wc.cancel != nil {
					oldCancel := wc.cancel
					wc.cancel = nil
					wc.ctx = nil
					wc.running = false
					wc.generation++ // mark old goroutine superseded before unlock
					if len(newUnion) == 0 {
						wc.status.Set("idle", "no active focuses")
					}
					wc.mu.Unlock()
					oldCancel()
					if len(newUnion) > 0 {
						wc.mu.Lock()
						wc.startLocked()
						wc.mu.Unlock()
					}
					continue
				}
			}
		}
		wc.mu.Unlock()
	}
}
