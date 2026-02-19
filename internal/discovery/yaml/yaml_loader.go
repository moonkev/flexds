package yaml

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/moonkev/flexds/internal/common/config"
	"github.com/moonkev/flexds/internal/common/types"
	"github.com/moonkev/flexds/internal/discovery"
	"go.yaml.in/yaml/v2"
)

type Config struct {
	ConfigPath string
}

type Route struct {
	MatchType        string `yaml:"match_type"`
	PathPrefix       string `yaml:"path_prefix"`
	PrefixRewrite    string `yaml:"prefix_rewrite"`
	RegexRewrite     string `yaml:"regex_rewrite"`
	RegexReplacement string `yaml:"regex_replacement"`
	HeaderName       string `yaml:"header_name"`
	HeaderValue      string `yaml:"header_value"`
	Http2            bool   `yaml:"http2"`
	Tls              bool   `yaml:"tls"`
}

type Service struct {
	Name      string `yaml:"name"`
	Instances []struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"instances"`
	Routes         []Route         `yaml:"routes"`
	Http2          bool            `yaml:"http2"`
	Tls            bool            `yaml:"tls"`
	DnsRefreshRate config.Duration `yaml:"dns_refresh_rate"`
}

func parseRoutes(service *Service) []types.RoutePattern {

	var routes = make([]types.RoutePattern, 0, len(service.Routes))
	for routeNum, route := range service.Routes {
		slog.Debug("parsing route", "loader", "yaml", "service", service.Name, "route_num", routeNum)
		rp := types.RoutePattern{
			Name:             fmt.Sprintf("%s-route-%d", service.Name, routeNum),
			MatchType:        route.MatchType,
			PathPrefix:       route.PathPrefix,
			PrefixRewrite:    route.PrefixRewrite,
			RegexRewrite:     route.RegexRewrite,
			RegexReplacement: route.RegexReplacement,
			HeaderName:       route.HeaderName,
			HeaderValue:      route.HeaderValue,
			Hosts:            []string{"*"},
		}

		routes = append(routes, rp)
	}
	return routes
}

func LoadConfig(config Config, aggregator *discovery.DiscoveredServiceAggregator) error {

	rawYaml, err := os.ReadFile(config.ConfigPath)
	if err != nil {
		return err
	}

	var services []Service
	var discoveredServices []*types.DiscoveredService

	err = yaml.Unmarshal(rawYaml, &services)
	if err != nil {
		return err
	}

	for _, svc := range services {
		instances := make([]types.ServiceInstance, 0)
		for _, inst := range svc.Instances {
			instances = append(instances, types.ServiceInstance{
				Address: inst.Host,
				Port:    inst.Port,
			})
		}

		routes := parseRoutes(&svc)

		discoveredServices = append(discoveredServices, &types.DiscoveredService{
			Name:           svc.Name,
			Instances:      instances,
			Routes:         routes,
			EnableHTTP2:    svc.Http2,
			EnableTLS:      svc.Tls,
			DnsRefreshRate: svc.DnsRefreshRate.ToDuration(),
		})
	}
	slog.Info("Loaded services from YAML config",
		"count", len(discoveredServices))
	for i, ds := range discoveredServices {
		slog.Info("Discovered service",
			"index", i,
			"name", ds.Name,
			"instances", ds.Instances,
			"routes", ds.Routes,
			"http2", ds.EnableHTTP2)
	}
	return aggregator.UpdateServices("yaml_loader", discoveredServices)
}
