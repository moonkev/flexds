package watcher

import (
	"context"
	"time"

	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	consulapi "github.com/hashicorp/consul/api"
)

// ServiceChangeHandler is called when services change
type ServiceChangeHandler func(services []string) error

// ConsulWatcher defines the interface for watching Consul service changes
type ConsulWatcher interface {
	// Watch starts watching Consul for service changes
	// It blocks until context is cancelled
	Watch(ctx context.Context) error
}

// WatcherConfig holds shared configuration for all watchers
type WatcherConfig struct {
	Client      *consulapi.Client
	Cache       cachev3.SnapshotCache
	WaitTimeSec int
	Handler     ServiceChangeHandler
}

// NewWatcher creates a watcher with the specified strategy
func NewWatcher(strategy string, cfg *WatcherConfig) ConsulWatcher {
	switch strategy {
	case "debounce":
		return NewDebounceWatcher(cfg, 500*time.Millisecond)
	case "batch":
		return NewBatchWatcher(cfg, 5, 1*time.Second)
	case "immediate":
		fallthrough
	default:
		return NewImmediateWatcher(cfg)
	}
}
