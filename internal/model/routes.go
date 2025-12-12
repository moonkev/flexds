package model

import (
	"log"
	"strconv"
	"strings"

	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	consulapi "github.com/hashicorp/consul/api"
)

// ParseServiceRoutes reads service metadata to generate multiple routing patterns.
// Supported metadata keys format: route_N_fieldname where N is a number (1, 2, 3...)
// For each route N:
//   - route_N_match_type: "path", "header", or "both" (default: "path")
//   - route_N_path_prefix: path prefix to match (e.g., "/api/v1/services/py-web")
//   - route_N_header_name: header name to match (e.g., "X-Service")
//   - route_N_header_value: header value to match (e.g., "py-web")
//   - route_N_prefix_rewrite: what to rewrite the matched prefix to (e.g., "/")
//   - route_N_hosts: comma-separated list of domains (e.g., "api.example.com,api2.example.com")
//
// ParseServiceRoutes reads service metadata to generate multiple routing patterns
func ParseServiceRoutes(entry *consulapi.ServiceEntry) []RoutePattern {
	svc := entry.Service.Service
	var routes []RoutePattern

	// If no metadata, create a default route with wildcard domain (accepts any Host header)
	if entry.Service.Meta == nil || len(entry.Service.Meta) == 0 {
		return []RoutePattern{{
			Name:       svc + "-default",
			MatchType:  "path",
			PathPrefix: "/svc/" + svc,
			Hosts:      []string{"*"},
		}}
	}

	// Parse numbered routes from metadata using underscore format: route_N_fieldname
	routeMap := make(map[string]map[string]string) // routeMap[routeNum][key] = value
	for key, value := range entry.Service.Meta {
		if strings.HasPrefix(key, "route_") {
			parts := strings.SplitN(key, "_", 3)
			if len(parts) == 3 {
				routeNum := parts[1]
				fieldName := parts[2]
				if routeMap[routeNum] == nil {
					routeMap[routeNum] = make(map[string]string)
				}
				routeMap[routeNum][fieldName] = value
			}
		}
	}

	// If no numbered routes, create default
	if len(routeMap) == 0 {
		return []RoutePattern{{
			Name:       svc + "-default",
			MatchType:  "path",
			PathPrefix: "/svc/" + svc,
			Hosts:      []string{svc + ".service.consul"},
		}}
	}

	// Build RoutePattern objects from the map
	for routeNum := 1; routeNum <= 10; routeNum++ { // Support up to 10 routes
		routeNumStr := strconv.Itoa(routeNum)
		routeConfig, exists := routeMap[routeNumStr]
		if !exists {
			continue
		}

		rp := RoutePattern{
			Name:      svc + "-route" + routeNumStr,
			MatchType: "path", // default
		}

		if v, ok := routeConfig["match_type"]; ok {
			rp.MatchType = v
		}
		if v, ok := routeConfig["path_prefix"]; ok {
			rp.PathPrefix = v
		}
		if v, ok := routeConfig["header_name"]; ok {
			rp.HeaderName = v
		}
		if v, ok := routeConfig["header_value"]; ok {
			rp.HeaderValue = v
		}
		if v, ok := routeConfig["prefix_rewrite"]; ok {
			rp.PrefixRewrite = v
		}
		if v, ok := routeConfig["hosts"]; ok {
			hosts := strings.Split(v, ",")
			for _, h := range hosts {
				if h = strings.TrimSpace(h); h != "" {
					rp.Hosts = append(rp.Hosts, h)
				}
			}
		}

		// Set defaults if not provided
		if rp.PathPrefix == "" {
			rp.PathPrefix = "/svc/" + svc
		}
		// Default to wildcard domain (accepts any Host header) if not specified
		if len(rp.Hosts) == 0 {
			rp.Hosts = []string{"*"}
		}

		routes = append(routes, rp)
		log.Printf("[PARSE ROUTES] service=%s route=%s match_type=%s path=%s header=%s:%s hosts=%v",
			svc, rp.Name, rp.MatchType, rp.PathPrefix, rp.HeaderName, rp.HeaderValue, rp.Hosts)
	}

	// If still no routes, return default
	if len(routes) == 0 {
		routes = []RoutePattern{{
			Name:       svc + "-default",
			MatchType:  "path",
			PathPrefix: "/svc/" + svc,
			Hosts:      []string{"*"},
		}}
	}

	return routes
}

// ParseServiceRouting reads service metadata and tags to generate routing rules
// DEPRECATED: Use ParseServiceRoutes instead
func ParseServiceRouting(entry *consulapi.ServiceEntry) (pathPrefix string, hosts []string, prefixRewrite string, headerMatchers []*route.HeaderMatcher) {
	// defaults
	svc := entry.Service.Service
	pathPrefix = "/svc/" + svc
	hosts = []string{svc + ".service.consul"}

	if entry.Service.Meta != nil {
		if v, ok := entry.Service.Meta["route.path_prefix"]; ok && v != "" {
			pathPrefix = v
		}
		if v, ok := entry.Service.Meta["route.host"]; ok && v != "" {
			// comma-separated
			parts := strings.Split(v, ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					hosts = append(hosts, p)
				}
			}
		}
		if v, ok := entry.Service.Meta["route.strip_prefix"]; ok && (strings.ToLower(v) == "true" || v == "1") {
			prefixRewrite = "/"
		}
		if v, ok := entry.Service.Meta["route.prefix_rewrite"]; ok && v != "" {
			prefixRewrite = v
		}
	}

	// parse tags for header matchers
	for _, t := range entry.Service.Tags {
		if strings.HasPrefix(t, "route-header:") {
			rest := strings.TrimPrefix(t, "route-header:")
			// expecting Name=Value
			parts := strings.SplitN(rest, "=", 2)
			if len(parts) == 2 {
				name := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if name != "" && val != "" {
					hm := &route.HeaderMatcher{
						Name: name,
						HeaderMatchSpecifier: &route.HeaderMatcher_ExactMatch{
							ExactMatch: val,
						},
					}
					headerMatchers = append(headerMatchers, hm)
				}
			}
		}
	}

	return
}
