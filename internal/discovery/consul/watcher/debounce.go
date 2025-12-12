package watcher

import (
	"context"
	"log"
	"time"

	consulapi "github.com/hashicorp/consul/api"
)

// DebounceWatcher batches rapid changes with a debounce timer
type DebounceWatcher struct {
	cfg              *WatcherConfig
	debounceInterval time.Duration
}

// NewDebounceWatcher creates a new debounce watcher
func NewDebounceWatcher(cfg *WatcherConfig, debounceInterval time.Duration) *DebounceWatcher {
	return &DebounceWatcher{
		cfg:              cfg,
		debounceInterval: debounceInterval,
	}
}

// Watch starts watching Consul and applies updates with debouncing
func (w *DebounceWatcher) Watch(ctx context.Context) error {
	var lastIndex uint64
	var pendingUpdate bool
	var latestServices []string

	debounceTimer := time.NewTimer(0)
	debounceTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[WATCHER:DEBOUNCE] stopping, context cancelled")
			debounceTimer.Stop()
			return nil

		case <-debounceTimer.C:
			// Debounce period expired - apply the update now
			log.Printf("[WATCHER:DEBOUNCE] debounce timer fired, applying batched update with %d services", len(latestServices))
			pendingUpdate = false
			if err := w.cfg.Handler(latestServices); err != nil {
				log.Printf("[WATCHER:DEBOUNCE] handler error: %v", err)
			}

		default:
			queryOpts := &consulapi.QueryOptions{
				WaitIndex: lastIndex,
				WaitTime:  time.Duration(w.cfg.WaitTimeSec) * time.Second,
			}
			queryOpts = queryOpts.WithContext(ctx)

			services, meta, err := w.cfg.Client.Catalog().Services(queryOpts)
			if err != nil {
				if ctx.Err() != nil {
					log.Printf("[WATCHER:DEBOUNCE] stopping, context cancelled")
					debounceTimer.Stop()
					return nil
				}
				log.Printf("[WATCHER:DEBOUNCE] error fetching services: %v", err)
				time.Sleep(1 * time.Second)
				continue
			}

			if meta.LastIndex == lastIndex {
				continue
			}

			log.Printf("[WATCHER:DEBOUNCE] detected change: lastIndex=%d newIndex=%d", lastIndex, meta.LastIndex)
			lastIndex = meta.LastIndex
			latestServices = filterServices(services)

			if !pendingUpdate {
				// First change detected - start debounce timer
				log.Printf("[WATCHER:DEBOUNCE] starting debounce timer (%v)", w.debounceInterval)
				pendingUpdate = true
				debounceTimer.Reset(w.debounceInterval)
			} else {
				// Another change while debounce is active - reset timer
				log.Printf("[WATCHER:DEBOUNCE] resetting debounce timer, more changes coming")
				debounceTimer.Reset(w.debounceInterval)
			}
		}
	}
}
