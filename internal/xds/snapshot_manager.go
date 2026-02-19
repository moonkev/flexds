package xds

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	commondns "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/common/dns/v3"
	dnscluster "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/dns/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tls "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	upstreamhttp "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	xdstype "github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/moonkev/flexds/internal/common/telemetry"
	types2 "github.com/moonkev/flexds/internal/common/types"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

var version uint64 = 1

type Config struct {
	Cache         cachev3.SnapshotCache
	ListenerPorts []uint32
}

type SnapshotManager struct {
	cache         cachev3.SnapshotCache
	listenerPorts []uint32
}

func NewSnapshotManager(config Config) *SnapshotManager {
	return &SnapshotManager{
		cache:         config.Cache,
		listenerPorts: config.ListenerPorts,
	}
}

// BuildAndPushSnapshot constructs XDS configuration from discovered services and pushes to Cache
func (s *SnapshotManager) BuildAndPushSnapshot(services []*types2.DiscoveredService) {
	var clusters []types.Resource
	var endpoints []types.Resource
	var routes []types.Resource
	var listeners []types.Resource
	allRoutes := make([]*route.Route, 0)

	slog.Info("Building snapshot", "count", len(services))

	for _, svc := range services {
		if len(svc.Instances) == 0 || len(svc.Routes) == 0 {
			slog.Info("Service has no healthy instances or configured routes", "service", svc.Name)
			continue
		}

		slog.Debug("Adding service", "service", svc.Name, "instances", len(svc.Instances))

		clusterName := svc.Name

		// Endpoints - build load assignment with hostname and listenerPorts
		lbs := make([]*endpoint.LbEndpoint, 0, len(svc.Instances))

		for _, inst := range svc.Instances {
			if inst.Address == "" {
				continue
			}
			slog.Debug("Adding endpoint", "service", svc.Name, "address", inst.Address, "listenerPorts", inst.Port)
			lb := &endpoint.LbEndpoint{
				HostIdentifier: &endpoint.LbEndpoint_Endpoint{
					Endpoint: &endpoint.Endpoint{
						Address: &core.Address{
							Address: &core.Address_SocketAddress{
								SocketAddress: &core.SocketAddress{
									Address:       inst.Address,
									PortSpecifier: &core.SocketAddress_PortValue{PortValue: uint32(inst.Port)},
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

		// Create DnsCluster configuration
		// AllAddressesInSingleEndpoint=false gives STRICT_DNS semantics (each address is a separate endpoint)
		dnsClusterConfig := &dnscluster.DnsCluster{
			DnsLookupFamily:              commondns.DnsLookupFamily_V4_ONLY,
			RespectDnsTtl:                true,
			AllAddressesInSingleEndpoint: false,
		}
		if svc.DnsRefreshRate > 0 {
			dnsClusterConfig.DnsRefreshRate = durationpb.New(svc.DnsRefreshRate)
			dnsClusterConfig.RespectDnsTtl = false
		}
		dnsClusterAny, err := anypb.New(dnsClusterConfig)
		if err != nil {
			slog.Error("Failed to marshal DnsCluster config", "error", err)
			continue
		}

		// Cluster using ClusterType extension point with DnsCluster
		cl := &cluster.Cluster{
			Name:           clusterName,
			ConnectTimeout: durationpb.New(2 * time.Second),
			ClusterDiscoveryType: &cluster.Cluster_ClusterType{
				ClusterType: &cluster.Cluster_CustomClusterType{
					Name:        "envoy.clusters.dns",
					TypedConfig: dnsClusterAny,
				},
			},
			LoadAssignment: cla,
			LbPolicy:       cluster.Cluster_ROUND_ROBIN,
		}

		// Add HTTP/2 protocol options if the service specifies http2 metadata or is detected as gRPC
		if svc.EnableHTTP2 {
			slog.Debug("configuring HTTP/2 support", "service", svc.Name)
			httpOpts := &upstreamhttp.HttpProtocolOptions{
				UpstreamProtocolOptions: &upstreamhttp.HttpProtocolOptions_ExplicitHttpConfig_{
					ExplicitHttpConfig: &upstreamhttp.HttpProtocolOptions_ExplicitHttpConfig{
						ProtocolConfig: &upstreamhttp.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
							Http2ProtocolOptions: &core.Http2ProtocolOptions{},
						},
					},
				},
			}
			httpOptsAny, err := anypb.New(httpOpts)
			if err != nil {
				panic(err)
			}
			cl.TypedExtensionProtocolOptions = map[string]*anypb.Any{
				"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": httpOptsAny,
			}
		}

		if svc.EnableTLS {
			slog.Debug("configuring TLS support", "service", svc.Name)

			// Set ALPN based on whether HTTP/2 is enabled
			var alpnProtocols []string
			if svc.EnableHTTP2 {
				alpnProtocols = []string{"h2", "http/1.1"}
			} else {
				alpnProtocols = []string{"http/1.1"}
			}

			tlsContext := &tls.UpstreamTlsContext{
				CommonTlsContext: &tls.CommonTlsContext{
					AlpnProtocols: alpnProtocols,
					ValidationContextType: &tls.CommonTlsContext_ValidationContext{
						ValidationContext: &tls.CertificateValidationContext{
							TrustChainVerification: tls.CertificateValidationContext_ACCEPT_UNTRUSTED,
						},
					},
				},
			}
			tlsContextAny, err := anypb.New(tlsContext)
			if err != nil {
				panic(err)
			}
			cl.TransportSocket = &core.TransportSocket{
				Name: "envoy.transport_sockets.tls",
				ConfigType: &core.TransportSocket_TypedConfig{
					TypedConfig: tlsContextAny,
				},
			}
		}

		clusters = append(clusters, cl)

		// Convert route patterns to routes
		for _, rp := range svc.Routes {
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
				slog.Debug("configuring regex rewrite", "service", svc.Name, "pattern", regexRewrite, "substitution", regexReplacement)
			} else if prefixRewrite != "" {
				ra.PrefixRewrite = prefixRewrite
				slog.Debug("configuring prefix rewrite", "service", svc.Name, "prefixRewrite", prefixRewrite)
			}

			routeMatch := &route.RouteMatch{
				PathSpecifier: &route.RouteMatch_Prefix{Prefix: pathPrefix},
			}

			if matchType == "header" || matchType == "both" {
				if headerName != "" && headerValue != "" {
					routeMatch.Headers = []*route.HeaderMatcher{{
						Name: headerName,
						HeaderMatchSpecifier: &route.HeaderMatcher_StringMatch{
							StringMatch: &matcher.StringMatcher{
								MatchPattern: &matcher.StringMatcher_Exact{Exact: headerValue},
							},
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
		slog.Warn("No services with healthy instances, pushing empty snapshot")
		snap, err := cachev3.NewSnapshot(fmt.Sprintf("%d", atomic.AddUint64(&version, 1)), map[resource.Type][]types.Resource{})
		if err != nil {
			slog.Error("Failed creating empty snapshot", "error", err)
			return
		}
		err = s.cache.SetSnapshot(context.Background(), "__REFERENCE_SNAPSHOT__", snap)
		if err != nil {
			slog.Error("Failed setting empty reference snapshot", "error", err)
		}
		nodeIDs := s.cache.GetStatusKeys()
		for _, nodeID := range nodeIDs {
			if err := s.cache.SetSnapshot(context.Background(), nodeID, snap); err != nil {
				slog.Error("Failed setting empty snapshot", "nodeID", nodeID, "error", err)
			}
		}
		slog.Info("Empty snapshot pushed")
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
		slog.Error("Failed to marshal HCM", "error", err)
		return
	}

	for _, listenerPort := range s.listenerPorts {
		ln := &listener.Listener{
			Name: fmt.Sprintf("listener_%d", listenerPort),
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						Address:       "0.0.0.0",
						PortSpecifier: &core.SocketAddress_PortValue{PortValue: listenerPort},
					},
				},
			},
			FilterChains: []*listener.FilterChain{{
				Filters: []*listener.Filter{{
					Name:       xdstype.HTTPConnectionManager,
					ConfigType: &listener.Filter_TypedConfig{TypedConfig: hcmAny},
				}},
			}},
		}
		listeners = append(listeners, ln)
	}

	// Build snapshot
	snapVer := fmt.Sprintf("%d", atomic.AddUint64(&version, 1))
	snap, err := cachev3.NewSnapshot(snapVer, map[resource.Type][]types.Resource{
		resource.ClusterType:  clusters,
		resource.EndpointType: endpoints,
		resource.RouteType:    routes,
		resource.ListenerType: listeners,
	})

	if err != nil {
		slog.Error("Failed to create snapshot", "error", err)
		return
	}

	err = s.cache.SetSnapshot(context.Background(), "__REFERENCE_SNAPSHOT__", snap)
	if err != nil {
		slog.Error("Failed setting reference snapshot", "error", err)
	}
	nodeIDs := s.cache.GetStatusKeys()
	slog.Debug("node IDs", "nodeIDs", nodeIDs)

	for _, nodeID := range nodeIDs {
		err = s.cache.SetSnapshot(context.Background(), nodeID, snap)
		if err != nil {
			slog.Error("Failed setting snapshot", "nodeID", nodeID, "error", err)
		}
	}
	slog.Info("Snapshot pushed",
		"version", snapVer,
		"listeners", len(listeners),
		"clusters", len(clusters),
		"endpoints", len(endpoints),
		"routes", len(routes),
		"virtualHosts", len(virtualHosts))
	telemetry.MetricSnapshotsPushed.Inc()
}
