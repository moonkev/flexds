package consul

import (
	"context"
	"log"
	"sort"

	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/moonkev/flexds/internal/discovery"
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

		var discoveredServices []*discovery.DiscoveredService

		for _, svc := range services {
			entries, _, err := client.Health().Service(svc, "", true, nil)
			if err != nil {
				log.Printf("[CONSUL HANDLER] error fetching healthy entries for service %s: %v", svc, err)
				continue
			}
			if len(entries) == 0 {
				log.Printf("[CONSUL HANDLER] service %s has no healthy instances", svc)
				continue
			}

			// Sort entries by Service.ModifyIndex in reverse order (highest first)
			// This ensures we use metadata from the most recently modified service instance
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Service.ModifyIndex > entries[j].Service.ModifyIndex
			})

			// Convert Consul entries to discovery model
			instances := make([]*discovery.ServiceInstance, 0, len(entries))
			for _, e := range entries {
				addr := e.Service.Address
				if addr == "" {
					addr = e.Node.Address
				}
				if addr == "" {
					continue
				}
				instances = append(instances, &discovery.ServiceInstance{
					Address: addr,
					Port:    e.Service.Port,
				})
			}
			var enableHttp2 bool

			// Check explicit http2 metadata setting from the most recently modified entry
			if len(entries) > 0 {
				metadata := entries[0].Service.Meta
				if val, ok := metadata["http2"]; ok && val == "true" {
					enableHttp2 = true
				}
			}

			// Parse routes from the most recently modified entry's metadata
			var routes []discovery.RoutePattern
			if len(entries) > 0 {
				routes = ParseServiceRoutes(entries[0])
			}

			discoveredServices = append(discoveredServices, &discovery.DiscoveredService{
				Name:        svc,
				Instances:   instances,
				Routes:      routes,
				EnableHTTP2: enableHttp2,
			})
		}

		xds.BuildAndPushSnapshot(cache, discoveredServices)
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
