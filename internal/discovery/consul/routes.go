package consul

import (
	"log"
	"strconv"
	"strings"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/moonkev/flexds/internal/xds"
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
func ParseServiceRoutes(entry *consulapi.ServiceEntry) []xds.RoutePattern {
	svc := entry.Service.Service
	var routes []xds.RoutePattern

	// If no metadata, create a default route with wildcard domain (accepts any Host header)
	if len(entry.Service.Meta) == 0 {
		return []xds.RoutePattern{{
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
		return []xds.RoutePattern{{
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

		rp := xds.RoutePattern{
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
		// Support legacy prefix_rewrite
		if v, ok := routeConfig["prefix_rewrite"]; ok {
			rp.PrefixRewrite = v
		}
		// Support regex_rewrite with pattern and replacement
		if v, ok := routeConfig["regex_rewrite"]; ok {
			rp.RegexRewrite = v
		}
		if v, ok := routeConfig["regex_replacement"]; ok {
			rp.RegexReplacement = v
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
		log.Printf("[PARSE ROUTES] service=%s route=%s match_type=%s path=%s prefix_rewrite=%q header=%s:%s hosts=%v",
			svc, rp.Name, rp.MatchType, rp.PathPrefix, rp.PrefixRewrite, rp.HeaderName, rp.HeaderValue, rp.Hosts)
	}

	// If still no routes, return default
	if len(routes) == 0 {
		routes = []xds.RoutePattern{{
			Name:       svc + "-default",
			MatchType:  "path",
			PathPrefix: "/svc/" + svc,
			Hosts:      []string{"*"},
		}}
	}

	return routes
}
