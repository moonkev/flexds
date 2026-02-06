package yaml

import (
	"log/slog"
	"os"

	"github.com/moonkev/flexds/internal/discovery"
	"github.com/moonkev/flexds/internal/types"
	"go.yaml.in/yaml/v2"
)

type Config struct {
	ConfigPath string
}

type Service struct {
	Name      string `yaml:"name"`
	Instances []struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"instances"`
	Meta  map[string]string `yaml:"meta"`
	Http2 bool              `yaml:"http2"`
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
		routes := discovery.ParseServiceRoutes(svc.Name, svc.Meta)
		discoveredServices = append(discoveredServices, &types.DiscoveredService{
			Name:        svc.Name,
			Instances:   instances,
			Routes:      routes,
			EnableHTTP2: svc.Http2,
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
