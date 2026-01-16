package consul

import (
	"context"
	"log"

	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/moonkev/flexds/internal/discovery/consul/watcher"
	"github.com/moonkev/flexds/internal/server"
	"github.com/moonkev/flexds/internal/xds"
)

// Config holds the application configuration
type ConsulConfig struct {
	ConsulAddr      string
	WaitTimeSec     int
	WatcherStrategy string // "immediate", "debounce", or "batch"
}

// WatchConsulBlocking watches for changes in Consul service catalog using the configured watcher strategy
// strategy can be "immediate", "debounce", or "batch"
func WatchConsulBlocking(ctx context.Context, client *consulapi.Client, cache cachev3.SnapshotCache, cfg *ConsulConfig) {
	// Create the service change handler that will be called when services change
	handler := func(services []string) error {
		log.Printf("[CONSUL HANDLER] processing %d services: %v", len(services), services)
		server.MetricServicesDiscovered.Set(float64(len(services)))
		entryMap := make(map[string][]*consulapi.ServiceEntry)
		for _, svc := range services {
			entries, _, err := client.Health().Service(svc, "", true, nil)
			if err != nil {
				log.Printf("[CONSUL HANDLER] error fetching healthy entires for service %s: %v", svc, err)
			}
			entryMap[svc] = entries
		}

		xds.BuildAndPushSnapshot(cache, entryMap, &routeBuilder{})
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
	routes := ParseServiceRoutes(entry)

	// Convert to interface{} slice for interface compatibility
	result := make([]interface{}, len(routes))
	for i, r := range routes {
		result[i] = r
	}
	return result
}
