package watcher

import (
	"context"
	"log/slog"
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
			slog.Info("Stopping debounce watcher, context cancelled")
			debounceTimer.Stop()
			return nil

		case <-debounceTimer.C:
			// Debounce period expired - apply the update now
			slog.Info("Debounce timer fired, applying batched update", "services", len(latestServices))
			pendingUpdate = false
			if err := w.cfg.Handler(latestServices); err != nil {
				slog.Error("handler error", "error", err)
			}

		default:
			queryOpts := &consulapi.QueryOptions{
				WaitIndex: lastIndex,
				WaitTime:  time.Duration(w.cfg.WaitTimeSec) * time.Second,
			}
			queryOpts = queryOpts.WithContext(ctx)

			serviceMapping, meta, err := w.cfg.Client.Catalog().Services(queryOpts)
			if err != nil {
				if ctx.Err() != nil {
					slog.Info("Stopping debounce watcher, context cancelled")
					debounceTimer.Stop()
					return nil
				}
				slog.Error("Failed to fetch services", "error", err)
				time.Sleep(1 * time.Second)
				continue
			}

			if meta.LastIndex == lastIndex {
				continue
			}

			slog.Info("Detected change", "lastIndex", lastIndex, "newIndex", meta.LastIndex)
			lastIndex = meta.LastIndex

			// Extract service names from the map keys
			latestServices = make([]string, 0, len(serviceMapping))
			for serviceName := range serviceMapping {
				latestServices = append(latestServices, serviceName)
			}

			if !pendingUpdate {
				// First change detected - start debounce timer
				slog.Info("Starting debounce timer", "interval", w.debounceInterval)
				pendingUpdate = true
				debounceTimer.Reset(w.debounceInterval)
			} else {
				// Reset timer as another change while debounce is active
				slog.Info("Resetting debounce timer, more changes coming")
				debounceTimer.Reset(w.debounceInterval)
			}
		}
	}
}
