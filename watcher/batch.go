package watcher

import (
	"context"
	"log"
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
	var latestServices []string

	batchTimer := time.NewTimer(0)
	batchTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[WATCHER:BATCH] stopping, context cancelled")
			batchTimer.Stop()
			return nil

		case <-batchTimer.C:
			if batchCount > 0 {
				log.Printf("[WATCHER:BATCH] batch timeout, applying %d changes with %d services", batchCount, len(latestServices))
				if err := w.cfg.Handler(latestServices); err != nil {
					log.Printf("[WATCHER:BATCH] handler error: %v", err)
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

			services, meta, err := w.cfg.Client.Catalog().Services(queryOpts)
			if err != nil {
				if ctx.Err() != nil {
					log.Printf("[WATCHER:BATCH] stopping, context cancelled")
					batchTimer.Stop()
					return nil
				}
				log.Printf("[WATCHER:BATCH] error fetching services: %v", err)
				time.Sleep(1 * time.Second)
				continue
			}

			if meta.LastIndex == lastIndex {
				continue
			}

			log.Printf("[WATCHER:BATCH] detected change: lastIndex=%d newIndex=%d", lastIndex, meta.LastIndex)
			lastIndex = meta.LastIndex
			latestServices = filterServices(services)
			batchCount++

			log.Printf("[WATCHER:BATCH] change detected, batch count: %d/%d", batchCount, w.maxBatchSize)

			if batchCount >= w.maxBatchSize {
				// Batch is full - apply immediately
				log.Printf("[WATCHER:BATCH] batch limit reached, applying snapshot")
				if err := w.cfg.Handler(latestServices); err != nil {
					log.Printf("[WATCHER:BATCH] handler error: %v", err)
				}
				batchCount = 0
				batchTimer.Stop()
			} else {
				// Start timer if not already running
				if batchCount == 1 {
					log.Printf("[WATCHER:BATCH] starting batch timer (%v)", w.batchTimeout)
					batchTimer.Reset(w.batchTimeout)
				}
			}
		}
	}
}
