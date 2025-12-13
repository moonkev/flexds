# FlexDS

An Envoy XDS (Extensible Discovery Service) server that bridges Consul service discovery with Envoy proxy, enabling dynamic configuration, flexible routing, and automatic service mesh management.

## Quick Start

### Prerequisites
- Go 1.24+
- Podman or Docker
- (Optional) Consul, Envoy, and test services running locally

### Build & Run

```bash
# Build the binary
go build -o flexds ./cmd/flexds

# Run standalone (requires Consul at localhost:8500)
./flexds -consul localhost:8500 -ads-port 18000 -admin-port 19005
```

The XDS server will start on **port 18000** and begin streaming configuration to connected Envoy proxies.

### Quick Start with Docker Compose

For a complete integrated setup (Consul cluster, Envoy, REST & gRPC services):

```bash
cd container
podman compose up --build -d

# View logs
podman compose logs -f

# Access services
curl http://localhost:18080/hello-service        # REST via proxy
grpcurl -plaintext localhost:18080 list          # gRPC via proxy
```

## Architecture

```
+-------------------------------------------------------------+
|         flexds-net (container bridge)                       |
|                                                             |
| +---------------+  +-----------------+  +---------------+   |
| | Consul Cluster|  | flexds XDS      |  | Envoy Proxy   |   |
| | 3 servers +   |->| watches Consul  |->| port 18080    |   |
| | 1 agent       |  | port 18000 (ADS)|  | admin 19000   |   |
| | 8500-8502     |  | port 19005      |  |               |   |
| | 18500         |  |                 |  |               |   |
| +---------------+  +-----------------+  +-------+-------+   |
|                                                 |           |
|         +---------------------------------------+           |
|         |                                       |           |
|   +-----+---------+                +------------+------+    |
|   | REST Services |                | gRPC Services     |    |
|   | (FastAPI)     |                | (Node.js)         |    |
|   |               |                |                   |    |
|   | rest-1: 8080  |                | grpc-1: 9090      |    |
|   | rest-2: 8080  |                | grpc-2: 9090      |    |
|   |               |                |                   |    |
|   | Route:        |                | Route:            |    |
|   | /hello-svc    |                | X-Service:grpc    |    |
|   +---------------+                +-------------------+    |
|                                                             |
+-------------------------------------------------------------+
```

## Features

### 🔄 Dynamic Service Discovery
- Real-time watching of Consul service catalog via blocking queries
- Automatic health filtering (only healthy instances included)
- Multi-instance load balancing with STRICT_DNS cluster resolution
- Zero-downtime service registration/deregistration via service metadata

### 🎯 Flexible Multi-Route Routing
Support multiple independent routes per service with metadata-driven configuration:

- **Path-Based** — Route by URL path prefix (e.g., `/hello-service`)
- **Header-Based** — Route by HTTP header values (e.g., `X-Service: grpc-service`)
- **Combined** — Require both path AND header match
- **Prefix Rewriting** — Rewrite path before sending to upstream

Each route supports independent configuration, enabling multiple access patterns from a single service instance.

### 📡 XDS Protocol Implementation
- **Discovery Mode**: ADS (Aggregated Discovery Service) with gRPC
- **Services**: LDS (Listeners), CDS (Clusters), EDS (Endpoints), RDS (Routes)
- **Clusters**: STRICT_DNS with LoadAssignment for hostname resolution
- **HTTP Support**: HTTP/1.1 and HTTP/2 auto-detection, explicit HTTP/2 opt-in via metadata
- **gRPC Support**: Native gRPC service proxying with proper HTTP/2 configuration

### 🛡️ Robust Error Handling
- Service health validation (only healthy endpoints included)
- Proper cluster/endpoint lifecycle management
- Graceful shutdown with deregistration
- Comprehensive logging with component-based filtering

### 🚀 Self-Managed Services
- Services register themselves with Consul on startup
- Services include routing rules in registration metadata
- Automatic deregistration on graceful shutdown
- Environment variable-based dynamic configuration

## Configuration

### Port Mapping

| Port  | Purpose                | Protocol | Component          | Notes |
|-------|------------------------|----------|--------------------|-------|
| 8500-8502 | Consul HTTP API  | HTTP     | Consul servers     | 1 server per host port |
| 18500 | Consul Agent API       | HTTP     | Consul agent       | External access point |
| 18000 | XDS gRPC Server        | gRPC     | flexds             | Envoy connects here |
| 19005 | flexds Admin/Metrics   | HTTP     | flexds             | `/metrics`, `/healthz` |
| 18080 | Envoy Listener         | HTTP/2   | Envoy              | Service requests arrive here |
| 19000 | Envoy Admin Console    | HTTP     | Envoy              | Stats, config inspection |
| 8080-8081 | REST Services      | HTTP     | Services           | 2 instances for LB testing |
| 9090-9091 | gRPC Services      | gRPC     | Services           | 2 instances for LB testing |

### Environment Variables

**flexds binary**:
```bash
-consul string          Consul address (default "localhost:8500")
-ads-port int          XDS server port (default 18000)
-admin-port int        Admin port (default 19005)
```

**Service Environment** (set by compose.yaml):
```bash
CONSUL_HOST            Consul agent hostname (e.g., consul-agent)
CONSUL_PORT            Consul agent port (8500)
SERVICE_NAME           Service name registered in Consul (e.g., hello-service)
SERVICE_ID             Unique service ID (e.g., service-name:port)
SERVICE_PORT           Port service is listening on
HOSTNAME               Container name used as service address in Consul
```

### Envoy Bootstrap Configuration

See `container/envoy-bootstrap.yaml`:
- **Node ID**: `ingress-gateway`
- **XDS Server**: `flexds:18000`
- **Listener Port**: `0.0.0.0:10000` (mapped to host port 18080)
- **Discovery Mode**: ADS with automatic protocol detection
- **Cluster Type**: STRICT_DNS with hostname resolution


## Multi-Route Routing Guide

Define flexible routing rules in Consul service metadata. Services register these rules automatically when they start, enabling dynamic route configuration without Envoy restarts.

### Metadata Format

Configure routes in Consul service metadata using this format:

```
dns_refresh_rate          = "30"                      # DNS refresh interval in seconds
route_N_match_type        = "path" | "header" | "both"
route_N_path_prefix       = "/path/to/service"
route_N_header_name       = "X-Header-Name"
route_N_header_value      = "header-value"
route_N_prefix_rewrite    = "/"
```

Where `N` is a number (1, 2, 3, ...) for each route. Supports up to 10 routes per service.

**Important**: Consul metadata keys use underscores: `route_1_match_type` ✅ (not `route.1.match_type` ❌)

### Example 1: REST Service - Path-Based Routing

The included REST services register themselves:

```bash
# Registered automatically with:
{
  "ID": "hello-service-1",
  "Name": "hello-service",
  "Port": 8080,
  "Address": "rest-service-1",
  "Meta": {
    "dns_refresh_rate": "30",
    "route_1_match_type": "path",
    "route_1_path_prefix": "/hello-service",
    "route_1_prefix_rewrite": "/",
    "route_2_match_type": "header",
    "route_2_header_name": "X-Service",
    "route_2_header_value": "hello-service",
    "route_2_path_prefix": "/",
    "route_2_prefix_rewrite": "/"
  }
}
```

**Usage**:
```bash
# Route 1: Path-based
curl http://localhost:18080/hello-service/hello

# Route 2: Header-based
curl -H "X-Service: hello-service" http://localhost:18080/hello

# Query parameters
curl "http://localhost:18080/hello-service/hello?name=Alice"
```

### Example 2: gRPC Service - Header-Based Routing

The gRPC services register with HTTP/2 support:

```bash
# Registered automatically with:
{
  "ID": "grpc-service-1",
  "Name": "grpc-service",
  "Port": 9090,
  "Address": "grpc-service-1",
  "Meta": {
    "http2": "true",
    "dns_refresh_rate": "30",
    "route_1_match_type": "header",
    "route_1_header_name": "X-Service",
    "route_1_header_value": "grpc-service",
    "route_1_path_prefix": "/",
    "route_1_prefix_rewrite": "/"
  }
}
```

**Usage**:
```bash
# Route via header
grpcurl -plaintext \
  -H "X-Service: grpc-service" \
  localhost:18080 \
  grpc_streaming.StreamingService/Health
```

### Example 3: Custom Service Registration

To register your own service with custom routing rules:

```bash
curl -X PUT http://localhost:18500/v1/agent/service/register \
  -H "Content-Type: application/json" \
  -d '{
    "ID": "my-service-1",
    "Name": "my-service",
    "Port": 3000,
    "Address": "my-service-container",
    "Meta": {
      "dns_refresh_rate": "60",
      "route_1_match_type": "path",
      "route_1_path_prefix": "/api/my-service",
      "route_1_prefix_rewrite": "/api",
      "route_2_match_type": "header",
      "route_2_header_name": "X-Service",
      "route_2_header_value": "my-service",
      "route_2_path_prefix": "/",
      "route_2_prefix_rewrite": "/"
    }
  }'
```

**Usage**:
```bash
# Path-based
curl http://localhost:18080/api/my-service/status

# Header-based
curl -H "X-Service: my-service" http://localhost:18080/data
```

### Example 4: Combined Path AND Header Matching

Require **both** path AND header to match:

```json
{
  "ID": "secure-service-1",
  "Name": "secure-service",
  "Port": 5000,
  "Address": "secure-service",
  "Meta": {
    "route_1_match_type": "both",
    "route_1_path_prefix": "/api/admin",
    "route_1_header_name": "X-Admin-Token",
    "route_1_header_value": "valid-token",
    "route_1_prefix_rewrite": "/admin"
  }
}
```

**Usage**:
```bash
# ✅ Matches (both conditions met)
curl -H "X-Admin-Token: valid-token" http://localhost:18080/api/admin/users

# ❌ Does NOT match (missing header)
curl http://localhost:18080/api/admin/users

# ❌ Does NOT match (wrong path)
curl -H "X-Admin-Token: valid-token" http://localhost:18080/users
```

## Implementation Details

### STRICT_DNS Cluster Discovery

Services use STRICT_DNS clusters for hostname resolution instead of EDS (Endpoint Discovery Service):

```go
// Cluster configured with STRICT_DNS
cluster.Type = core.Cluster_STRICT_DNS
cluster.LoadAssignment = &endpoint.ClusterLoadAssignment{
    ClusterName: serviceName,
    Endpoints: []{
        {
            LbEndpoints: []{
                Address: "rest-service-1:8080",  // Hostname preserved
                // ... port, weight, etc
            },
        },
    },
}
```

**Why STRICT_DNS?**
- Preserves service hostnames for container DNS resolution
- Resolves once per refresh interval (configurable via `dns_refresh_rate` metadata)
- Load-balances across resolved addresses
- Works seamlessly with container networks

### Single Wildcard Virtual Host Architecture

All routes are consolidated into a single virtual host with wildcard domain:

```go
vhHost := &route.VirtualHost{
    Name:    "default",
    Domains: []string{"*"},  // Matches any host
    Routes:  allRoutes,      // All service routes merged here
}
```

**Why this design?**
- Simplifies routing (all requests to any host)
- Avoids Envoy validation errors about multiple wildcards
- Routes evaluated in order—first match wins
- All services accessible via single listener

### HTTP/2 Protocol Support

Services can opt-in to HTTP/2 via metadata:

```json
"http2": "true"  // Enable HTTP/2 for this service
```

The XDS server then configures:
- Per-cluster HTTP/2 protocol options
- Listener CodecType: AUTO for automatic protocol negotiation
- Proper gRPC support via HTTP/2 framing

### Service Metadata Registration

Services self-register with Consul on startup, including routing rules in metadata. flexds watches Consul and automatically:
1. Detects service registration/deregistration
2. Parses route metadata
3. Builds new XDS snapshot
4. Pushes to Envoy via ADS stream

No manual configuration needed—services own their routing rules.

## Monitoring & Debugging

### Quick Health Checks

```bash
# Consul cluster
curl http://localhost:8500/v1/status/leader
curl http://localhost:18500/v1/catalog/services

# flexds metrics
curl http://localhost:19005/metrics
curl http://localhost:19005/healthz

# Envoy listener
curl http://localhost:19000/clusters | grep -i hello

# Services
curl http://localhost:8080/health
curl http://localhost:8081/health
grpcurl -plaintext localhost:9090 list
grpcurl -plaintext localhost:9091 list
```

### Logging

flexds logs include component-based filtering:

```bash
# View all logs
podman compose logs flexds

# Filter for specific patterns
podman compose logs flexds | grep -i "snapshot"
podman compose logs flexds | grep -i "endpoint"
podman compose logs flexds | grep -i "route"

# Watch logs live
podman compose logs -f flexds
```

### Verify Configuration Delivery

1. **Check Envoy config**:
   ```bash
   curl http://localhost:19000/config_dump | jq '.configs[] | .dynamic_listeners[0].active_state.listener.route_config.virtual_hosts[0].routes[0:3]'
   ```

2. **Check endpoint health**:
   ```bash
   curl http://localhost:19000/clusters | grep -A 10 "hello-service"
   curl http://localhost:19000/clusters | grep -A 10 "grpc-service"
   ```

3. **Check registered services in Consul**:
   ```bash
   curl http://localhost:18500/v1/catalog/services | jq .
   curl http://localhost:18500/v1/catalog/service/hello-service | jq .
   ```

### Test Load Balancing

```bash
# REST service load balancing (should alternate between -1 and -2)
for i in {1..10}; do
  curl -s http://localhost:18080/hello-service/info | grep '"name"'
done

# Check logs to see which instance handled each request
podman compose logs rest-service-1 | grep "INFO"
podman compose logs rest-service-2 | grep "INFO"
```

### Test Routing

```bash
# Test path-based routing
curl http://localhost:18080/hello-service/hello?name=Alice

# Test header-based routing
curl -H "X-Service: hello-service" http://localhost:18080/hello

# Test gRPC header routing
grpcurl -plaintext \
  -H "X-Service: grpc-service" \
  localhost:18080 \
  grpc_streaming.StreamingService/Health
```

## Troubleshooting

### Services Won't Start

**Check logs**:
```bash
podman compose logs --tail=50 rest-service-1
podman compose logs --tail=50 grpc-service-1
podman compose logs --tail=50 flexds
```

**Common issues**:
- Port conflicts (8080, 9090 already in use)
- Insufficient memory or CPU
- Image build failures (check `podman compose build`)

### Services Register But Don't Appear in Envoy

**Verify registration**:
```bash
# Check Consul has services
curl http://localhost:18500/v1/catalog/services

# Check specific service
curl http://localhost:18500/v1/catalog/service/hello-service | jq '.[] | {ID, Address, Port, Meta}'

# Check Envoy config
curl http://localhost:19000/config_dump | jq '.configs[] | .dynamic_listeners[0].active_state.listener.route_config.virtual_hosts[0].routes | length'
```

**If not appearing**:
- Ensure services are healthy: `curl http://localhost:18500/v1/health/service/hello-service`
- Check flexds is watching Consul: `podman compose logs flexds | grep -i "consul\|watch"`
- Verify flexds connected to Envoy: `podman compose logs flexds | grep -i "stream"`

### Routes Not Matching Requests

**Verify metadata**:
```bash
# Check registered service metadata
curl http://localhost:18500/v1/catalog/service/hello-service | jq '.[0].ServiceMeta'

# Check Envoy has routes
curl http://localhost:19000/config_dump | jq '.configs[] | .dynamic_listeners[0].active_state.listener.route_config.virtual_hosts[0].routes[] | {match: .match.prefix, action: .route.cluster}'
```

**Common issues**:
- Typo in metadata keys (must use underscores, not dots)
- Metadata not updated yet (flexds watches with delays)
- Routes in wrong order (first match wins)

### Load Balancing Not Working

**Check endpoint distribution**:
```bash
# View endpoints per cluster
curl http://localhost:19000/clusters | grep -A 20 "hello-service"
```

**Verify both instances registered**:
```bash
curl http://localhost:18500/v1/catalog/service/hello-service | jq '.[] | {ID, Address, Port}'
```

**Test requests**:
```bash
# Run multiple requests and check logs
for i in {1..5}; do
  curl -s http://localhost:18080/hello-service/info
  podman compose logs rest-service-1 --tail=1
  podman compose logs rest-service-2 --tail=1
done
```

### Envoy Can't Reach flexds

**Check connection**:
```bash
# Verify flexds is running
podman compose ps | grep flexds

# Check flexds logs for errors
podman compose logs flexds | grep -i "error\|panic"

# Verify Envoy logs
podman compose logs envoy | grep -i "upstream\|connect"
```

**Common issues**:
- flexds not started yet (wait a few seconds)
- flexds crashed on startup (check logs for Go panics)
- Network issue (verify all on same network)

### gRPC Requests Failing

**Verify HTTP/2**:
```bash
# Check service registered with http2: true
curl http://localhost:18500/v1/catalog/service/grpc-service | jq '.[0].ServiceMeta.http2'

# Verify Envoy has HTTP/2 configured
curl http://localhost:19000/config_dump | jq '.configs[] | .dynamic_clusters[] | select(.cluster.name=="grpc-service") | .cluster.http2_protocol_options'
```

**Test direct gRPC**:
```bash
# This should work
grpcurl -plaintext localhost:9090 list

# This should work with header
grpcurl -plaintext -H "X-Service: grpc-service" localhost:18080 list
```

**Common issues**:
- Service not registered with `http2: true` metadata
- Envoy not configured for HTTP/2
- Port mapping error (9090 not exposed)

## Project Structure

```
flexds/
├── cmd/
│   └── flexds/
│       └── main.go           # Application entry point
├── internal/
│   ├── xds/
│   │   └── config.go         # XDS snapshot building
│   ├── discovery/
│   │   └── consul/
│   │       └── discovery.go  # Consul service watching
│   ├── model/
│   │   ├── types.go          # Data structures
│   │   └── routes.go         # Route parsing logic
│   └── server/
│       └── server.go         # gRPC server
├── container/
│   ├── compose.yaml          # Docker Compose orchestration
│   ├── Containerfile         # flexds OCI image
│   ├── envoy-bootstrap.yaml  # Envoy static config
│   ├── rest-service/         # Python FastAPI service
│   └── grpc-service/         # Node.js gRPC service
├── go.mod
├── main.go                   # Legacy (use cmd/flexds/main.go)
└── README.md
```

## Next Steps

1. **Start the services**: `cd container && podman compose up --build -d`
2. **Verify health**: Run health check commands above
3. **Test routing**: Try the routing examples with curl/grpcurl
4. **Monitor**: Watch Envoy config and metrics as requests flow through
5. **Add services**: Create new services with self-registration
6. **Explore**: Check Consul UI, Envoy admin, and flexds metrics

## Architecture Highlights

- **Dynamic Discovery**: Services register themselves; flexds watches Consul automatically
- **STRICT_DNS**: Hostnames preserved for container networking; DNS resolved per refresh interval
- **HTTP/2 Support**: Automatic detection with explicit opt-in via metadata for gRPC
- **Metadata-Driven**: Services define their own routes; flexds generates XDS from metadata
- **Multi-Instance**: Load balancing across multiple service instances via Envoy
- **Graceful Shutdown**: Services deregister from Consul on termination

## References

- [Envoy XDS Documentation](https://www.envoyproxy.io/docs/envoy/latest/api-docs/xds_protocol)
- [Envoy Admin API](https://www.envoyproxy.io/docs/envoy/latest/operations/admin)
- [Consul Service Catalog](https://www.consul.io/docs/services)
- [fastcache (XDS caching library)](https://github.com/envoyproxy/go-control-plane)
- [gRPC Documentation](https://grpc.io/)
- [Container Images](https://podman.io/)
