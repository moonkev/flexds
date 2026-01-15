package xds

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync/atomic"
	"time"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	xdstype "github.com/envoyproxy/go-control-plane/pkg/wellknown"
	consulapi "github.com/hashicorp/consul/api"
	"github.com/moonkev/flexds/internal/server"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

var version uint64 = 1

// RouteBuilder interface for dependency injection
type RouteBuilder interface {
	BuildRoutes(entry *consulapi.ServiceEntry) []interface{}
}

// GetHealthyInstances uses the health API to fetch passing instances
func GetHealthyInstances(client *consulapi.Client, service string) ([]*consulapi.ServiceEntry, error) {
	entries, _, err := client.Health().Service(service, "", true, nil)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// ShouldEnableHTTP2 checks if HTTP/2 should be enabled for this service
// Reads from metadata field "http2" (values: "true" or "false")
// Requires explicit configuration - no port-based detection since ports can be randomized
func ShouldEnableHTTP2(entry *consulapi.ServiceEntry) bool {
	if entry == nil || entry.Service == nil || entry.Service.Meta == nil {
		return false
	}

	// Check explicit http2 metadata setting
	if val, ok := entry.Service.Meta["http2"]; ok {
		return val == "true"
	}

	// Default to false - HTTP/2 must be explicitly enabled via metadata
	return false
}

// GetDNSRefreshRate extracts the dns_refresh_rate from service metadata
// Returns the duration in seconds (default 60 seconds if not specified)
func GetDNSRefreshRate(entry *consulapi.ServiceEntry) time.Duration {
	if entry == nil || entry.Service == nil || entry.Service.Meta == nil {
		return 60 * time.Second // default
	}

	if val, ok := entry.Service.Meta["dns_refresh_rate"]; ok {
		if seconds, err := strconv.ParseInt(val, 10, 64); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return 60 * time.Second // default
}

// BuildAndPushSnapshot constructs XDS configuration from discovered services and pushes to cache
func BuildAndPushSnapshot(cache cachev3.SnapshotCache, client *consulapi.Client, services []string, routeBuilder RouteBuilder) {
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
		var parsedForRouting *consulapi.ServiceEntry
		parsedForRouting = instances[0]

		// Endpoints - build load assignment with hostname and port
		lbs := make([]*endpoint.LbEndpoint, 0, len(instances))

		for _, e := range instances {
			addr := e.Service.Address
			if addr == "" {
				addr = e.Node.Address
			}
			// Only replace with node address if service address is empty
			// (don't replace valid hostnames with node IP)
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

		// Get DNS refresh rate from metadata (in seconds)
		dnsRefreshRate := GetDNSRefreshRate(instances[0])

		// Cluster (STRICT_DNS for hostname resolution)
		// Load assignment provides the hostname and port for DNS resolution
		cl := &cluster.Cluster{
			Name:           clusterName,
			ConnectTimeout: durationpb.New(2 * time.Second),
			ClusterDiscoveryType: &cluster.Cluster_Type{
				Type: cluster.Cluster_STRICT_DNS,
			},
			LoadAssignment:  cla,
			LbPolicy:        cluster.Cluster_ROUND_ROBIN,
			DnsLookupFamily: cluster.Cluster_V4_ONLY,
			DnsRefreshRate:  durationpb.New(dnsRefreshRate),
		}

		// Add HTTP/2 protocol options if the service specifies http2 metadata or is detected as gRPC
		if ShouldEnableHTTP2(instances[0]) {
			log.Printf("[CLUSTER] service=%s configured with HTTP/2 support", svc)
			cl.Http2ProtocolOptions = &core.Http2ProtocolOptions{}
		}

		clusters = append(clusters, cl)

		// Parse service routing patterns
		routePatterns := routeBuilder.BuildRoutes(parsedForRouting)

		// Convert patterns to routes - patterns are RoutePattern objects from model package
		for _, rpInterface := range routePatterns {
			// Assert to model.RoutePattern struct
			rp, ok := rpInterface.(RoutePattern)
			if !ok {
				log.Printf("[BUILD SNAPSHOT] warning: failed to assert route pattern to RoutePattern type")
				continue
			}

			pathPrefix := rp.PathPrefix
			matchType := rp.MatchType
			headerName := rp.HeaderName
			headerValue := rp.HeaderValue
			prefixRewrite := rp.PrefixRewrite
			regexRewrite := rp.RegexRewrite
			regexReplacement := rp.RegexReplacement

			ra := &route.RouteAction{
				ClusterSpecifier: &route.RouteAction_Cluster{Cluster: clusterName},
			}

			// Apply rewrite: regex_rewrite takes priority, then legacy prefix_rewrite
			if regexRewrite != "" {
				ra.RegexRewrite = &matcher.RegexMatchAndSubstitute{
					Pattern: &matcher.RegexMatcher{
						Regex: regexRewrite,
					},
					Substitution: regexReplacement,
				}
				log.Printf("[ROUTE] service=%s regex_rewrite(pattern=%q substitution=%q)", svc, regexRewrite, regexReplacement)
			} else if prefixRewrite != "" {
				ra.PrefixRewrite = prefixRewrite
				log.Printf("[ROUTE] service=%s prefix_rewrite=%q", svc, prefixRewrite)
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

	// Create a single virtual host
	var virtualHosts []*route.VirtualHost
	if len(allRoutes) > 0 {
		vhHost := &route.VirtualHost{
			Name:    "default",
			Domains: []string{"*"},
			Routes:  allRoutes,
		}
		virtualHosts = append(virtualHosts, vhHost)
	}

	// If no services, push an empty snapshot
	if len(clusters) == 0 {
		log.Printf("[BUILD SNAPSHOT] no services with healthy instances, pushing empty snapshot")
		snap, err := cachev3.NewSnapshot(fmt.Sprintf("%d", atomic.AddUint64(&version, 1)), map[resource.Type][]types.Resource{})
		if err != nil {
			log.Printf("error creating empty snapshot: %v", err)
			return
		}
		err = cache.SetSnapshot(context.Background(), "__REFERENCE_SNAPSHOT__", snap)
		if err != nil {
			log.Printf("[SNAPSHOT STORE] error setting empty reference snapshot: %v", err)
		}
		nodeIDs := cache.GetStatusKeys()
		for _, nodeID := range nodeIDs {
			if err := cache.SetSnapshot(context.Background(), nodeID, snap); err != nil {
				log.Printf("[SNAPSHOT STORE] error setting empty snapshot for %s: %v", nodeID, err)
			}
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
		StatPrefix:           "ingress_http",
		CodecType:            hcm.HttpConnectionManager_AUTO,
		Http2ProtocolOptions: &core.Http2ProtocolOptions{},
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

	err = cache.SetSnapshot(context.Background(), "__REFERENCE_SNAPSHOT__", snap)
	if err != nil {
		log.Printf("[SNAPSHOT STORE] error setting reference snapshot: %v", err)
	}
	nodeIDs := cache.GetStatusKeys()
	log.Printf("[DEBUG]Node IDs: %s", nodeIDs)

	for _, nodeID := range nodeIDs {
		err = cache.SetSnapshot(context.Background(), nodeID, snap)
		if err != nil {
			log.Printf("[SNAPSHOT STORE] error setting snapshot for %s: %v", nodeID, err)
		}
	}
	log.Printf("[SNAPSHOT PUSHED] version=%s listeners=%d clusters=%d endpoints=%d routes=%d virtualHosts=%d",
		snapVer, len(listeners), len(clusters), len(endpoints), len(routes), len(virtualHosts))
	server.MetricSnapshotsPushed.Inc()
}
