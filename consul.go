package main

import (
	"context"
	"log"
	"time"

	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/moonkev/flexds/config"
	"github.com/moonkev/flexds/xds"
)

// WatchConsulBlocking watches for changes in Consul service catalog using blocking queries
func WatchConsulBlocking(ctx context.Context, client *consulapi.Client, cache cachev3.SnapshotCache, cfg *Config) {
	var lastIndex uint64

	for {
		select {
		case <-ctx.Done():
			log.Printf("[CONSUL WATCH] stopping, context cancelled")
			return
		default:
		}

		// Create a query options with context for interruptible queries
		queryOpts := &consulapi.QueryOptions{
			WaitIndex: lastIndex,
			WaitTime:  time.Duration(cfg.WaitTimeSec) * time.Second,
		}
		queryOpts = queryOpts.WithContext(ctx)

		services, meta, err := client.Catalog().Services(queryOpts)
		if err != nil {
			// Check if context was cancelled
			if ctx.Err() != nil {
				log.Printf("[CONSUL WATCH] stopping, context cancelled")
				return
			}
			log.Printf("[CONSUL WATCH] error fetching services: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		if meta.LastIndex == lastIndex {
			continue
		}
		log.Printf("[CONSUL WATCH] detected change: lastIndex=%d newIndex=%d", lastIndex, meta.LastIndex)
		lastIndex = meta.LastIndex

		svcList := make([]string, 0)
		for name := range services {
			if name != "consul" {
				svcList = append(svcList, name)
			}
		}

		log.Printf("[CONSUL WATCH] found %d services: %v", len(svcList), svcList)
		metricServicesDiscovered.Set(float64(len(svcList)))
		xds.BuildAndPushSnapshot(cache, client, svcList, "*", &routeBuilder{}, metricSnapshotsPushed)
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
