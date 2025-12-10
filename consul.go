package main

import (
	"context"
	"log"

	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/moonkev/flexds/config"
	"github.com/moonkev/flexds/watcher"
	"github.com/moonkev/flexds/xds"
)

// WatchConsulBlocking watches for changes in Consul service catalog using the configured watcher strategy
// strategy can be "immediate", "debounce", or "batch"
func WatchConsulBlocking(ctx context.Context, client *consulapi.Client, cache cachev3.SnapshotCache, cfg *Config) {
	// Create the service change handler that will be called when services change
	handler := func(services []string) error {
		log.Printf("[CONSUL HANDLER] processing %d services: %v", len(services), services)
		metricServicesDiscovered.Set(float64(len(services)))
		xds.BuildAndPushSnapshot(cache, client, services, "*", &routeBuilder{}, metricSnapshotsPushed)
		return nil
	}

	// Create the appropriate watcher based on configured strategy
	watcherCfg := &watcher.WatcherConfig{
		Client:      client,
		Cache:       cache,
		WaitTimeSec: cfg.WaitTimeSec,
		Handler:     handler,
	}

	// Get the watcher strategy from config (default to "immediate")
	strategy := cfg.WatcherStrategy
	if strategy == "" {
		strategy = "immediate"
	}

	w := watcher.NewWatcher(strategy, watcherCfg)
	log.Printf("[CONSUL WATCH] starting with strategy: %s", strategy)

	// Watch blocks until context is cancelled
	if err := w.Watch(ctx); err != nil {
		log.Printf("[CONSUL WATCH] error: %v", err)
	}
}

// routeBuilder implements xds.RouteBuilder interface
type routeBuilder struct{}

func (rb *routeBuilder) BuildRoutes(entry *consulapi.ServiceEntry) []interface{} {
	// Parse routes from Consul metadata
	routes := config.ParseServiceRoutes(entry)

	// Convert to interface{} slice for interface compatibility
	result := make([]interface{}, len(routes))
	for i, r := range routes {
		result[i] = r
	}
	return result
}
