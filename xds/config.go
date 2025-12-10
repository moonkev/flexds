package xds

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"time"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	xdstype "github.com/envoyproxy/go-control-plane/pkg/wellknown"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/moonkev/flexds/config"
	anypb "google.golang.org/protobuf/types/known/anypb"
	durationpb "google.golang.org/protobuf/types/known/durationpb"
)

var version uint64 = 1

// RouteBuilder interface for dependency injection
type RouteBuilder interface {
	BuildRoutes(entry *consulapi.ServiceEntry) []interface{}
}

// MetricCounter interface for metrics
type MetricCounter interface {
	Inc()
}

// GetHealthyInstances uses the health API to fetch passing instances
func GetHealthyInstances(client *consulapi.Client, service string) ([]*consulapi.ServiceEntry, error) {
	entries, _, err := client.Health().Service(service, "", true, nil)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// IsIPAddress checks if the given string is a valid IPv4 or IPv6 address
func IsIPAddress(addr string) bool {
	return net.ParseIP(addr) != nil
}

// BuildAndPushSnapshot constructs XDS configuration from discovered services and pushes to cache
func BuildAndPushSnapshot(cache cachev3.SnapshotCache, client *consulapi.Client, services []string, nodeID string, routeBuilder RouteBuilder, metricsPushed MetricCounter) {
	var clusters []types.Resource
	var endpoints []types.Resource
	var routes []types.Resource
	var listeners []types.Resource
	allRoutes := make([]*route.Route, 0)

	log.Printf("[BUILD SNAPSHOT] processing %d services", len(services))

	for _, svc := range services {
		instances, err := GetHealthyInstances(client, svc)
		if err != nil {
			log.Printf("error getting instances for %s: %v", svc, err)
			continue
		}
		if len(instances) == 0 {
			log.Printf("[BUILD SNAPSHOT] service %s has no healthy instances", svc)
			continue
		}

		log.Printf("[BUILD SNAPSHOT] adding service %s with %d instances", svc, len(instances))

		clusterName := svc

		// Cluster (EDS)
		cl := &cluster.Cluster{
			Name:                 clusterName,
			ConnectTimeout:       durationpb.New(2 * time.Second),
			ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_EDS},
			EdsClusterConfig: &cluster.Cluster_EdsClusterConfig{
				EdsConfig: &core.ConfigSource{
					ResourceApiVersion: core.ApiVersion_V3,
					ConfigSourceSpecifier: &core.ConfigSource_Ads{
						Ads: &core.AggregatedConfigSource{},
					},
				},
			},
			LbPolicy:        cluster.Cluster_ROUND_ROBIN,
			DnsLookupFamily: cluster.Cluster_V4_ONLY,
		}
		clusters = append(clusters, cl)

		// Endpoints
		lbs := make([]*endpoint.LbEndpoint, 0, len(instances))
		var parsedForRouting *consulapi.ServiceEntry
		parsedForRouting = instances[0]

		for _, e := range instances {
			addr := e.Service.Address
			if addr == "" {
				addr = e.Node.Address
			}
			if addr != "" && !IsIPAddress(addr) && e.Node.Address != "" {
				addr = e.Node.Address
			}
			if addr == "" {
				continue
			}
			log.Printf("[ENDPOINT] service=%s address=%s port=%d nodeAddr=%s svcAddr=%s",
				svc, addr, e.Service.Port, e.Node.Address, e.Service.Address)
			lb := &endpoint.LbEndpoint{
				HostIdentifier: &endpoint.LbEndpoint_Endpoint{
					Endpoint: &endpoint.Endpoint{
						Address: &core.Address{
							Address: &core.Address_SocketAddress{
								SocketAddress: &core.SocketAddress{
									Address:       addr,
									PortSpecifier: &core.SocketAddress_PortValue{PortValue: uint32(e.Service.Port)},
								},
							},
						},
					},
				},
			}
			lbs = append(lbs, lb)
		}

		cla := &endpoint.ClusterLoadAssignment{
			ClusterName: clusterName,
			Endpoints:   []*endpoint.LocalityLbEndpoints{{LbEndpoints: lbs}},
		}
		endpoints = append(endpoints, cla)

		// Parse service routing patterns
		routePatterns := routeBuilder.BuildRoutes(parsedForRouting)

		// Convert patterns to routes - patterns are RoutePattern objects from config package
		for _, rpInterface := range routePatterns {
			// Assert to config.RoutePattern struct
			rp, ok := rpInterface.(config.RoutePattern)
			if !ok {
				log.Printf("[BUILD SNAPSHOT] warning: failed to assert route pattern to RoutePattern type")
				continue
			}

			pathPrefix := rp.PathPrefix
			matchType := rp.MatchType
			headerName := rp.HeaderName
			headerValue := rp.HeaderValue
			prefixRewrite := rp.PrefixRewrite

			ra := &route.RouteAction{
				ClusterSpecifier: &route.RouteAction_Cluster{Cluster: clusterName},
			}
			if prefixRewrite != "" {
				ra.PrefixRewrite = prefixRewrite
			}

			routeMatch := &route.RouteMatch{
				PathSpecifier: &route.RouteMatch_Prefix{Prefix: pathPrefix},
			}

			if matchType == "header" || matchType == "both" {
				if headerName != "" && headerValue != "" {
					routeMatch.Headers = []*route.HeaderMatcher{{
						Name: headerName,
						HeaderMatchSpecifier: &route.HeaderMatcher_ExactMatch{
							ExactMatch: headerValue,
						},
					}}
				}
			}

			routeObj := &route.Route{
				Match:  routeMatch,
				Action: &route.Route_Route{Route: ra},
			}
			allRoutes = append(allRoutes, routeObj)
		}
	}

	// Create single virtual host
	var virtualHosts []*route.VirtualHost
	if len(allRoutes) > 0 {
		vhHost := &route.VirtualHost{
			Name:    "default",
			Domains: []string{"*"},
			Routes:  allRoutes,
		}
		virtualHosts = append(virtualHosts, vhHost)
	}

	// If no services, push empty snapshot
	if len(clusters) == 0 {
		log.Printf("[BUILD SNAPSHOT] no services with healthy instances, pushing empty snapshot")
		snap, err := cachev3.NewSnapshot(fmt.Sprintf("%d", atomic.AddUint64(&version, 1)), map[resource.Type][]types.Resource{})
		if err != nil {
			log.Printf("error creating empty snapshot: %v", err)
			return
		}
		if err := cache.SetSnapshot(context.Background(), nodeID, snap); err != nil {
			log.Printf("error setting empty snapshot: %v", err)
		}
		if err := cache.SetSnapshot(context.Background(), "ingress-gateway", snap); err != nil {
			log.Printf("[SNAPSHOT STORE] error setting empty snapshot for ingress-gateway: %v", err)
		}
		log.Printf("[SNAPSHOT PUSHED] empty snapshot")
		return
	}

	// Route config
	routeCfg := &route.RouteConfiguration{
		Name:         "local_route",
		VirtualHosts: virtualHosts,
	}
	routes = append(routes, routeCfg)

	// Listener
	hcmCfg := &hcm.HttpConnectionManager{
		StatPrefix: "ingress_http",
		RouteSpecifier: &hcm.HttpConnectionManager_Rds{
			Rds: &hcm.Rds{
				ConfigSource: &core.ConfigSource{
					ResourceApiVersion: core.ApiVersion_V3,
					ConfigSourceSpecifier: &core.ConfigSource_Ads{
						Ads: &core.AggregatedConfigSource{},
					},
				},
				RouteConfigName: "local_route",
			},
		},
		HttpFilters: []*hcm.HttpFilter{{
			Name: "envoy.filters.http.router",
			ConfigType: &hcm.HttpFilter_TypedConfig{
				TypedConfig: &anypb.Any{
					TypeUrl: "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router",
				},
			},
		}},
	}

	hcmAny, err := anypb.New(hcmCfg)
	if err != nil {
		log.Printf("failed to marshal hcm: %v", err)
		return
	}

	ln := &listener.Listener{
		Name:    "listener_0",
		Address: &core.Address{Address: &core.Address_SocketAddress{SocketAddress: &core.SocketAddress{Address: "0.0.0.0", PortSpecifier: &core.SocketAddress_PortValue{PortValue: 18080}}}},
		FilterChains: []*listener.FilterChain{{
			Filters: []*listener.Filter{{
				Name:       xdstype.HTTPConnectionManager,
				ConfigType: &listener.Filter_TypedConfig{TypedConfig: hcmAny},
			}},
		}},
	}
	listeners = append(listeners, ln)

	// Build snapshot
	snapVer := fmt.Sprintf("%d", atomic.AddUint64(&version, 1))
	snap, err := cachev3.NewSnapshot(snapVer, map[resource.Type][]types.Resource{
		resource.ClusterType:  clusters,
		resource.EndpointType: endpoints,
		resource.RouteType:    routes,
		resource.ListenerType: listeners,
	})
	if err != nil {
		log.Printf("error creating snapshot: %v", err)
		return
	}

	if err := cache.SetSnapshot(context.Background(), nodeID, snap); err != nil {
		log.Printf("error setting snapshot: %v", err)
	} else {
		log.Printf("[SNAPSHOT PUSHED] version=%s listeners=%d clusters=%d endpoints=%d routes=%d virtualHosts=%d",
			snapVer, len(listeners), len(clusters), len(endpoints), len(routes), len(virtualHosts))
		metricsPushed.Inc()

		if err := cache.SetSnapshot(context.Background(), "ingress-gateway", snap); err != nil {
			log.Printf("[SNAPSHOT STORE] error setting snapshot for ingress-gateway: %v", err)
		}

		retrievedSnap, err := cache.GetSnapshot(nodeID)
		if err != nil {
			log.Printf("[SNAPSHOT VERIFY] ERROR retrieving snapshot: %v", err)
		} else if retrievedSnap == nil {
			log.Printf("[SNAPSHOT VERIFY] WARNING: snapshot is nil after SetSnapshot")
		} else {
			log.Printf("[SNAPSHOT VERIFY] OK: snapshot retrieved successfully - version=%s listeners=%v clusters=%v endpoints=%v routes=%v",
				retrievedSnap.GetVersion(resource.ListenerType),
				len(retrievedSnap.GetResources(resource.ListenerType)),
				len(retrievedSnap.GetResources(resource.ClusterType)),
				len(retrievedSnap.GetResources(resource.EndpointType)),
				len(retrievedSnap.GetResources(resource.RouteType)))
		}
	}
}
