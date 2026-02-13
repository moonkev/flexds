package consul

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/moonkev/flexds/internal/discovery"
	"github.com/moonkev/flexds/internal/discovery/consul/watcher"
	"github.com/moonkev/flexds/internal/server"
	"github.com/moonkev/flexds/internal/types"
)

// Config Config holds the application configuration
type Config struct {
	ConsulAddr      string
	WaitTimeSec     int
	WatcherStrategy string // "immediate", "debounce", or "batch"
}

type HeaderRoundTripper struct {
	Rt http.RoundTripper
}

func (h *HeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return h.Rt.RoundTrip(req)
}

func NewClient(addr string) (*consulapi.Client, error) {
	consulCfg := consulapi.DefaultConfig()
	consulCfg.Address = fmt.Sprintf("http://%s", addr)

	consulCfg.HttpClient = &http.Client{
		Transport: &HeaderRoundTripper{Rt: http.DefaultTransport},
	}
	return consulapi.NewClient(consulCfg)
}

// WatchConsulBlocking watches for changes in the Consul service catalog using the configured watcher strategy
// selected strategy can be "immediate", "debounce", or "batch"
func WatchConsulBlocking(ctx context.Context, addr string, cfg *Config, aggregator *discovery.DiscoveredServiceAggregator) {

	client, err := NewClient(addr)
	if err != nil {
		slog.Error("failed to create consul client", "error", err)
		return
	}

	// Create the service change handler that will be called when services change
	handler := func(services []string) error {
		slog.Debug("Processing services", "count", len(services), "services", services)
		server.MetricServicesDiscovered.Set(float64(len(services)))

		var discoveredServices []*types.DiscoveredService

		for _, svc := range services {
			entries, _, err := client.Health().Service(svc, "", true, nil)
			if err != nil {
				slog.Error("Failed fetching healthy entries", "service", svc, "error", err)
				continue
			}
			if len(entries) == 0 {
				slog.Warn("Service has no healthy instances", "service", svc)
				continue
			}

			// Sort entries by Service.ModifyIndex in reverse order (highest first)
			// This ensures we use metadata from the most recently modified service instance
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Service.ModifyIndex > entries[j].Service.ModifyIndex
			})

			// Convert Consul entries to discovery model
			instances := make([]types.ServiceInstance, 0, len(entries))
			for _, e := range entries {
				addr := e.Service.Address
				if addr == "" {
					addr = e.Node.Address
				}
				if addr == "" {
					continue
				}
				instances = append(instances, types.ServiceInstance{
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
			var routes []types.RoutePattern
			if len(entries) > 0 {
				headEntry := entries[0]
				routes = ParseServiceRoutes(headEntry.Service.Service, headEntry.Service.Meta)
			}

			discoveredServices = append(discoveredServices, &types.DiscoveredService{
				Name:        svc,
				Instances:   instances,
				Routes:      routes,
				EnableHTTP2: enableHttp2,
			})
		}

		return aggregator.UpdateServices("consul_loader", discoveredServices)
	}

	// Create the appropriate watcher based on a configured strategy
	watcherCfg := &watcher.WatcherConfig{
		Client:      client,
		WaitTimeSec: cfg.WaitTimeSec,
		Handler:     handler,
	}

	// Get the watcher strategy from config (default to "immediate")
	strategy := cfg.WatcherStrategy
	if strategy == "" {
		strategy = "immediate"
	}

	w := watcher.NewWatcher(strategy, watcherCfg)
	slog.Info("Starting consul watch", "strategy", strategy)

	// Watch blocks until context is cancelled
	if err := w.Watch(ctx); err != nil {
		slog.Error("consul watch error", "error", err)
	}
}
