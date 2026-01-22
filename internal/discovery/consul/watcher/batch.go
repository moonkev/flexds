package watcher

import (
	"context"
	"log/slog"
	"time"

	consulapi "github.com/hashicorp/consul/api"
)

// BatchWatcher applies updates when batch size reached or timeout expires
type BatchWatcher struct {
	cfg          *WatcherConfig
	maxBatchSize int
	batchTimeout time.Duration
}

// NewBatchWatcher creates a new batch watcher
func NewBatchWatcher(cfg *WatcherConfig, maxBatchSize int, batchTimeout time.Duration) *BatchWatcher {
	return &BatchWatcher{
		cfg:          cfg,
		maxBatchSize: maxBatchSize,
		batchTimeout: batchTimeout,
	}
}

// Watch starts watching Consul and applies batched updates
func (w *BatchWatcher) Watch(ctx context.Context) error {
	var lastIndex uint64
	var batchCount int
	var services []string

	batchTimer := time.NewTimer(0)
	batchTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("stopping batch watcher, context cancelled")
			batchTimer.Stop()
			return nil

		case <-batchTimer.C:
			if batchCount > 0 {
				slog.Info("Batch timeout, applying changes", "changes", batchCount, "services", len(services))
				if err := w.cfg.Handler(services); err != nil {
					slog.Error("handler error", "error", err)
				}
				batchCount = 0
				batchTimer.Stop()
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
					slog.Info("Stopping batch watcher, context cancelled")
					batchTimer.Stop()
					return nil
				}
				slog.Error("Failed to fetch services", "error", err)
				time.Sleep(1 * time.Second)
				continue
			}

			if meta.LastIndex == lastIndex {
				continue
			}

			lastIndex = meta.LastIndex

			// Extract service names from the map keys
			services = make([]string, 0, len(serviceMapping))
			for serviceName := range serviceMapping {
				services = append(services, serviceName)
			}
			batchCount++

			slog.Info("Change detected", "batchCount", batchCount, "maxBatchSize", w.maxBatchSize)

			if batchCount >= w.maxBatchSize {
				// Batch is full - apply immediately
				slog.Info("Batch limit reached, applying snapshot")
				if err := w.cfg.Handler(services); err != nil {
					slog.Error("Error processing batch", "error", err)
				}
				batchCount = 0
				batchTimer.Stop()
			} else {
				// Start timer if not already running
				if batchCount == 1 {
					slog.Info("Starting batch timer", "timeout", w.batchTimeout)
					batchTimer.Reset(w.batchTimeout)
				}
			}
		}
	}
}
