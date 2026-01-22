package consul

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/moonkev/flexds/internal/discovery"
)

// ParseServiceRoutes reads service metadata to generate multiple routing patterns.
// Supported metadata keys format: route_N_fieldname where N is a number (1, 2, 3...)
// For each route N:
//   - route_N_match_type: "path", "header", or "both" (default: "path")
//   - route_N_path_prefix: path prefix to match (e.g., "/api/v1/services/py-web")
//   - route_N_header_name: header name to match (e.g., "X-Service")
//   - route_N_header_value: header value to match (e.g., "py-web")
//   - route_N_prefix_rewrite: what to rewrite the matched prefix to (e.g., "/")
//
// ParseServiceRoutes reads service metadata to generate multiple routing patterns
func ParseServiceRoutes(entry *consulapi.ServiceEntry) []discovery.RoutePattern {
	svc := entry.Service.Service
	var routes []discovery.RoutePattern

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
		return routes
	}

	// Build RoutePattern objects from the map
	for routeNum := 1; routeNum <= 10; routeNum++ { // Support up to 10 routes
		routeNumStr := strconv.Itoa(routeNum)
		routeConfig, exists := routeMap[routeNumStr]
		if !exists {
			continue
		}

		rp := discovery.RoutePattern{
			Name:      fmt.Sprintf("%s-route-%s", svc, routeNumStr),
			MatchType: "path",
			Hosts:     []string{"*"},
		}

		if v, ok := routeConfig["match_type"]; ok {
			rp.MatchType = v
		}
		if v, ok := routeConfig["header_name"]; ok {
			rp.HeaderName = v
		}
		if v, ok := routeConfig["header_value"]; ok {
			rp.HeaderValue = v
		}
		if v, ok := routeConfig["path_prefix"]; ok {
			rp.PathPrefix = v
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

		// Set defaults if not provided
		if rp.PathPrefix == "" {
			slog.Warn("No path prefix provided for route", "route", rp.Name)
			continue
		}

		routes = append(routes, rp)
		slog.Debug("Parse route",
			"service", svc,
			"route", rp.Name,
			"matchType", rp.MatchType,
			"path", rp.PathPrefix,
			"prefixRewrite", rp.PrefixRewrite,
			"header", rp.HeaderName+":"+rp.HeaderValue,
			"hosts", rp.Hosts)
	}

	return routes
}
