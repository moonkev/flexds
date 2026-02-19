package marathon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/moonkev/flexds/internal/common/types"
	"github.com/moonkev/flexds/internal/discovery"
)

type Config struct {
	URL                 string
	CredentialsFilePath string
	Interval            time.Duration
}

type marathonResponse struct {
	Apps []marathonApp `json:"apps"`
}

type marathonApp struct {
	ID              string                   `json:"id"`
	Ports           []int                    `json:"ports"`
	PortDefinitions []marathonPortDefinition `json:"portDefinitions"`
	Tasks           []marathonTask           `json:"tasks"`
	Labels          map[string]string        `json:"labels"`
}

type marathonPortDefinition struct {
	Port   int               `json:"port"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type marathonTask struct {
	ID                 string                       `json:"id"`
	Host               string                       `json:"host"`
	IPAddresses        []marathonIPAddress          `json:"ipAddresses"`
	Ports              []int                        `json:"ports"`
	HealthCheckResults []marathonHealthCheckResults `json:"healthCheckResults"`
	State              string                       `json:"state"`
}

type marathonIPAddress struct {
	IPAddress string `json:"ipAddress"`
	Protocol  string `json:"protocol"`
}

type marathonHealthCheckResults struct {
	Alive bool `json:"alive"`
}

func (t *marathonTask) IsHealthy() bool {
	if t.State != "TASK_RUNNING" || len(t.HealthCheckResults) == 0 {
		return false
	}
	for _, result := range t.HealthCheckResults {
		if result.Alive {
			return true
		}
	}
	return false
}

func LoadConfig(ctx context.Context, config Config, aggregator *discovery.DiscoveredServiceAggregator) error {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			slog.Debug("loading Marathon config")
			err := loadConfig(config, aggregator)
			if err != nil {
				slog.Error("failed to load Marathon config", "error", err)
				return err
			}
			timer.Reset(config.Interval)
		}
	}
}

func loadConfig(config Config, aggregator *discovery.DiscoveredServiceAggregator) error {

	var creds string
	httpClient := http.Client{Timeout: 10 * time.Second}

	url := fmt.Sprintf("%s/v2/apps?embed=apps.tasks", config.URL)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request in marathon loader: %w", err)
	}

	if config.CredentialsFilePath != "" {
		credsBytes, err := os.ReadFile(config.CredentialsFilePath)
		if err != nil {
			return fmt.Errorf("failed to read credentials file: %w", err)
		}
		creds = string(credsBytes)
		parts := strings.SplitN(strings.TrimSpace(creds), ":", 2)
		if len(parts) == 2 {
			req.SetBasicAuth(parts[0], parts[1])
		} else {
			return fmt.Errorf("invalid credentials format in %s", config.CredentialsFilePath)
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch from Marathon API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("marathon API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var marathonResp marathonResponse
	if err := json.Unmarshal(body, &marathonResp); err != nil {
		slog.Error("failed to parse Marathon response", "error", err, "url", url, "body", string(body))
		return fmt.Errorf("failed to parse Marathon response: %w", err)
	}

	discoveredServices := convertToDiscoveredServices(marathonResp.Apps)
	return aggregator.UpdateServices("marathon_loader", discoveredServices)
}

func convertToDiscoveredServices(apps []marathonApp) []*types.DiscoveredService {
	var serviceLen int
	for _, app := range apps {
		serviceLen += len(app.PortDefinitions)
	}

	services := make([]*types.DiscoveredService, 0, serviceLen)

	for _, app := range apps {

		// Filter to healthy tasks only
		healthyTasks := make([]marathonTask, 0, len(app.Tasks))
		for _, task := range app.Tasks {
			if task.IsHealthy() {
				healthyTasks = append(healthyTasks, task)
			}
		}
		if len(healthyTasks) == 0 {
			continue
		}

		for portIndex, portDef := range app.PortDefinitions {

			sanitizedAppId := strings.NewReplacer("/", "_", "-", "_").Replace(app.ID[1:])
			serviceName := fmt.Sprintf("mesos_%s_%s", sanitizedAppId, portDef.Name)
			instances := make([]types.ServiceInstance, 0, len(healthyTasks))
			for _, task := range healthyTasks {

				address := getTaskAddress(task)
				port := task.Ports[portIndex]

				instances = append(instances, types.ServiceInstance{
					Address: address,
					Port:    port,
				})
			}

			ds := &types.DiscoveredService{
				Name:      serviceName,
				Instances: instances,
				Routes:    buildRoutes(serviceName, portDef.Labels),
			}

			if portDef.Name == "grpc" || portDef.Labels["http2"] == "true" {
				ds.EnableHTTP2 = true
			}

			services = append(services, ds)
		}
	}

	return services
}

func getTaskAddress(task marathonTask) string {
	for _, ip := range task.IPAddresses {
		if ip.Protocol == "IPv4" && ip.IPAddress != "" {
			return ip.IPAddress
		}
	}
	return task.Host
}

func buildRoutes(serviceName string, labels map[string]string) []types.RoutePattern {
	routes := make([]types.RoutePattern, 0)
	var routingKey string
	if labelKey, ok := labels["routing_key"]; ok && labelKey != "" {
		routingKey = labelKey
	} else {
		routingKey = serviceName
	}

	prefixRoutePattern := types.RoutePattern{
		Name:          fmt.Sprintf("%s-route-prefix", serviceName),
		MatchType:     "path",
		PathPrefix:    fmt.Sprintf("/%s", routingKey),
		PrefixRewrite: "/",
	}
	routes = append(routes, prefixRoutePattern)

	headerRoutePattern := types.RoutePattern{
		Name:        fmt.Sprintf("%s-route-header", serviceName),
		MatchType:   "header",
		HeaderName:  "destination_service",
		HeaderValue: routingKey,
	}
	routes = append(routes, headerRoutePattern)

	return routes
}
