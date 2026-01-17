package watcher

import (
	"context"
	"log"
	"time"

	consulapi "github.com/hashicorp/consul/api"
)

// ImmediateWatcher applies updates as soon as they're detected
type ImmediateWatcher struct {
	cfg *WatcherConfig
}

// NewImmediateWatcher creates a new immediate watcher
func NewImmediateWatcher(cfg *WatcherConfig) *ImmediateWatcher {
	return &ImmediateWatcher{cfg: cfg}
}

// Watch starts watching Consul and immediately applies updates
func (w *ImmediateWatcher) Watch(ctx context.Context) error {
	var lastIndex uint64

	for {
		select {
		case <-ctx.Done():
			log.Printf("[WATCHER:IMMEDIATE] stopping, context cancelled")
			return nil
		default:
		}

		queryOpts := &consulapi.QueryOptions{
			WaitIndex: lastIndex,
			WaitTime:  time.Duration(w.cfg.WaitTimeSec) * time.Second,
		}
		queryOpts = queryOpts.WithContext(ctx)

		serviceMapping, meta, err := w.cfg.Client.Catalog().Services(queryOpts)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("[WATCHER:IMMEDIATE] stopping, context cancelled")
				return nil
			}
			log.Printf("[WATCHER:IMMEDIATE] error fetching services: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		if meta.LastIndex == lastIndex {
			continue
		}

		log.Printf("[WATCHER:IMMEDIATE] detected change: lastIndex=%d newIndex=%d", lastIndex, meta.LastIndex)
		lastIndex = meta.LastIndex

		// Extract service names from the map keys
		svcList := make([]string, 0, len(serviceMapping))
		for serviceName := range serviceMapping {
			svcList = append(svcList, serviceName)
		}
		log.Printf("[WATCHER:IMMEDIATE] found %d services: %v", len(svcList), svcList)

		if err := w.cfg.Handler(svcList); err != nil {
			log.Printf("[WATCHER:IMMEDIATE] handler error: %v", err)
		}
	}
}
