# Container Services

This directory contains the Docker/Podman Compose setup and test services for the flexds xDS control plane.

## Directory Structure

```
container/
├── compose.yaml                     # Main compose file (orchestrates all services)
├── Containerfile                    # OCI container image for flexds (Go)
├── envoy-bootstrap.yaml             # Envoy proxy static configuration
├── rest-service/                    # Python FastAPI REST service
│   ├── app.py
│   ├── Containerfile
│   └── README.md
├── grpc-service/                    # Node.js gRPC streaming service
│   ├── streaming.proto
│   ├── server.js
│   ├── package.json
│   ├── Containerfile
│   └── README.md
└── README.md                        # This file
```

## Services

### Consul (3-node cluster + 1 agent)

#### consul-server-1, consul-server-2, consul-server-3
- **Description**: High-availability Consul cluster for service registry and configuration
- **Deployment**: 3 servers with bootstrap-expect=3 for HA quorum
- **Ports**: 
  - `8500:8500` — Server 1 HTTP API (host port 8500)
  - `8501:8500` — Server 2 HTTP API (host port 8501)
  - `8502:8500` — Server 3 HTTP API (host port 8502)
  - `8600:8600/udp` — DNS interface (per server)
- **Network**: `flexds-net`
- **Image**: `consul:1.15.4`
- **Health**: Consul status check (10s interval)

#### consul-agent
- **Description**: Client agent for service registration and discovery
- **Role**: Local agent for services to register themselves
- **Ports**: 
  - `18500:8500` — HTTP API (external access from host)
  - `18600:8600/udp` — DNS interface
- **Network**: `flexds-net`
- **Image**: `consul:1.15.4`
- **Join Strategy**: Retries join to consul-server-1

### flexds
- **Description**: xDS control plane (Envoy Data Source) — watches Consul and serves configuration to Envoy via gRPC
- **Ports**: 
  - `18000:18000` — ADS (Aggregated Discovery Service) gRPC
  - `19005:19005` — Admin HTTP (metrics, health checks)
- **Network**: `flexds-net`
- **Build**: From repo root `Containerfile`, compiled Go binary
- **Configuration**: Connects to consul-agent:8500 for service discovery
- **Logs**: Watch for XDS snapshot updates and Consul watch activity

### Envoy
- **Description**: Service proxy — routes requests through dynamically configured listeners
- **Ports**:
  - `18080:10000` — Main proxy listener (HTTP with X-Service header routing)
  - `19000:19000` — Admin console (stats, config inspection)
- **Network**: `flexds-net`
- **Image**: `envoyproxy/envoy:distroless-v1.33.13`
- **Config**: Bootstrap from `envoy-bootstrap.yaml`, XDS from flexds
- **Features**: 
  - HTTP/1.1 and HTTP/2 auto-detection
  - STRICT_DNS clusters with hostname resolution
  - Path-based and header-based routing (via service metadata)

### rest-service-1, rest-service-2
- **Description**: Python FastAPI REST API for testing HTTP proxying with self-registration
- **Instances**: 2 instances for load balancing testing
  - rest-service-1: port 8080 (internal) → 8080 (host)
  - rest-service-2: port 8080 (internal) → 8081 (host)
- **Network**: `flexds-net`
- **Build**: From `rest-service/Containerfile`, FastAPI with uvicorn
- **Auto-registration**: Services register themselves with Consul on startup
- **Deregistration**: Graceful shutdown via lifespan context manager
- **Endpoints**:
  - `GET /health` — Health check
  - `GET /hello?name=World` — Hello message with optional name parameter
  - `GET /info` — Service metadata and endpoints
- **Service Name**: `hello-service` (registered in Consul)
- **Routing**: Path prefix `/hello-service` and header `X-Service: hello-service`

### grpc-service-1, grpc-service-2
- **Description**: Node.js gRPC service with streaming RPC methods and self-registration
- **Instances**: 2 instances for load balancing testing
  - grpc-service-1: port 9090 (internal) → 9090 (host)
  - grpc-service-2: port 9090 (internal) → 9091 (host)
- **Network**: `flexds-net`
- **Build**: From `grpc-service/Containerfile`, Node.js gRPC server
- **Auto-registration**: Services register themselves with Consul on startup
- **Lifecycle**: SIGTERM handler for graceful shutdown with deregistration
- **Endpoints**:
  - `Health()` — Unary RPC health check
  - `CounterStream(start, count)` — Server-streaming RPC returning integers
- **Service Name**: `grpc-service` (registered in Consul)
- **HTTP/2**: Explicitly marked via metadata for Envoy configuration
- **Routing**: Header `X-Service: grpc-service` for routing decisions

## Quick Start

### Build and Run from Container Directory

```bash
# From container/ directory
cd container
podman compose up --build -d

# Or with docker
docker compose up --build -d

# View logs for all services
podman compose logs -f

# Bring services down
podman compose down
```

### Run from Repository Root

```bash
# From project root
podman compose -f container/compose-consul.yaml up --build -d
```

## Testing Services

### REST Service

#### Direct Access (no proxy)

```bash
# Health check
curl http://localhost:8080/health

# Hello endpoint
curl http://localhost:8080/hello
curl http://localhost:8080/hello?name=Alice

# Service info
curl http://localhost:8080/info

# Access second instance (different port)
curl http://localhost:8081/health
```

#### Via Envoy Proxy (port 18080)

```bash
# Path-based routing to /hello-service prefix
curl -X GET http://localhost:18080/hello-service

# Header-based routing with X-Service header
curl -X GET http://localhost:18080/hello -H "X-Service: hello-service"

# With query parameters
curl -X GET "http://localhost:18080/hello-service?name=Bob"
```

### gRPC Service

#### Install grpcurl (if not installed)

```bash
# macOS
brew install grpcurl

# Or download from: https://github.com/fullstorydev/grpcurl/releases
```

#### Direct Access (no proxy)

```bash
# List available services
grpcurl -plaintext localhost:9090 list

# Health check (unary RPC)
grpcurl -plaintext localhost:9090 grpc_streaming.StreamingService/Health

# Counter streaming (server-streaming RPC)
# Streams integers from 10 to 19
grpcurl -plaintext -d '{"start": 10, "count": 10}' \
  localhost:9090 grpc_streaming.StreamingService/CounterStream

# Counter streaming with default values (0 to 9)
grpcurl -plaintext -d '{}' \
  localhost:9090 grpc_streaming.StreamingService/CounterStream

# Access second instance (different port)
grpcurl -plaintext localhost:9091 list
```

#### Via Envoy Proxy (port 18080)

```bash
# Using header-based routing
grpcurl -plaintext \
  -H "X-Service: grpc-service" \
  localhost:18080 list

# Health check
grpcurl -plaintext \
  -H "X-Service: grpc-service" \
  localhost:18080 grpc_streaming.StreamingService/Health

# Counter stream
grpcurl -plaintext \
  -H "X-Service: grpc-service" \
  -d '{"start": 5, "count": 5}' \
  localhost:18080 grpc_streaming.StreamingService/CounterStream
```

## Checking Service Status

### View Running Containers

```bash
podman compose ps
```

### Inspect Individual Service Logs

```bash
# Consul cluster
podman compose logs consul-server-1
podman compose logs consul-agent

# flexds control plane
podman compose logs flexds

# Envoy proxy
podman compose logs envoy

# Services
podman compose logs rest-service-1
podman compose logs rest-service-2
podman compose logs grpc-service-1
podman compose logs grpc-service-2
```

### Health Checks

```bash
# Consul cluster health
curl http://localhost:8500/v1/status/leader

# flexds admin/metrics
curl http://localhost:19005/healthz
curl http://localhost:19005/metrics

# Check registered services in Consul
curl http://localhost:8500/v1/catalog/services

# Check rest-service health
curl http://localhost:8080/health
curl http://localhost:8081/health

# Check grpc-service health
grpcurl -plaintext localhost:9090 grpc_streaming.StreamingService/Health
grpcurl -plaintext localhost:9091 grpc_streaming.StreamingService/Health
```

## Envoy Admin Console

Access Envoy's admin console at: **http://localhost:19000**

Useful endpoints:
- `http://localhost:19000/stats` — Service statistics and counters
- `http://localhost:19000/clusters` — Configured clusters and their health
- `http://localhost:19000/listeners` — Configured listeners and routes
- `http://localhost:19000/config_dump` — Full Envoy configuration (JSON)
- `http://localhost:19000/config_dump?format=json` — Pretty-printed JSON

## Service Discovery & Registration Flow

### Current Architecture

1. **Services Start**:
   - REST services (rest-service-1, rest-service-2) initialize FastAPI
   - gRPC services (grpc-service-1, grpc-service-2) initialize gRPC server
   - Both register with Consul once fully bound and ready to accept traffic

2. **Consul Agent**:
   - Local agent on consul-agent:8500 (external: localhost:18500)
   - Services register via HTTP PUT to `/v1/agent/service/register`
   - Services include routing metadata (path, headers, DNS refresh rate)

3. **flexds Control Plane**:
   - Watches Consul for service changes
   - Generates Envoy XDS configuration
   - Pushes updates to Envoy via gRPC (ADS)

4. **Envoy Proxy**:
   - Receives configuration from flexds
   - Creates clusters for each registered service
   - Uses STRICT_DNS for hostname resolution
   - Routes requests based on path and headers

### Service Metadata

Services register with the following metadata for configuration:

```json
{
  "route_1_match_type": "path",                // Path-based routing
  "route_1_path_prefix": "/hello-service",     // Route prefix
  "route_1_prefix_rewrite": "/",               // Rewrite prefix for upstream
  "route_2_match_type": "header",              // Header-based routing
  "route_2_header_name": "X-Service",          // Header name
  "route_2_header_value": "hello-service",     // Header value to match
  "http2": "true"                              // Enable HTTP/2 (gRPC only)
}
```

## Architecture Diagram

```
┌──────────────────────────────────────────────────────────────┐
│                            flexds-net (bridge)               │
│                                                              │
│  ┌──────────────┐  ┌───────────────┐  ┌────────────────┐     │
│  │ Consul Nodes │  │   flexds      │  │    Envoy       │     │
│  │ (3 servers)  │  │ XDS Server    │  │  Proxy         │     │
│  │ + 1 agent    │  │               │  │                │     │
│  │              │  │ :18000 (ADS)  │  │ :18080 (HTTP/2)│     │
│  │ :8500-8502   │  │ :19005 (admin)|  │ :19000 (admin) │     │
│  └──────────────┘  └───────────────┘  └────────────────┘     │
│         │                  │                │                │
│         └──────────────────┴────────────────┘                │
│                            │                                 │
│  ┌─────────────────────────┴──────────────────────────┐      │
│  │                                                    │      │
│  │  ┌────────────────┐  ┌────────────────┐            │      │
│  │  │  REST Service  │  │  gRPC Service  │            │      │
│  │  │  (FastAPI)     │  │  (Node.js)     │            │      │
│  │  │                │  │                │            │      │
│  │  │ -1: :8080 ──┐  │  │ -1: :9090 ──┐  │            │      │
│  │  │ -2: :8080 ──┤  │  │ -2: :9090 ──┤  │            │      │
│  │  │             │  │  │             │  │            │      │
│  │  │ Auto-register with Consul on startup            │      │
│  │  └────────────────┘  └────────────────┘            │      │
│  │                                                    │      │
│  └────────────────────────────────────────────────────┘      │
│                                                              │
└──────────────────────────────────────────────────────────────┘
                                    │
                        Host Ports Exposed:
                8500-8502, 18500, 18000, 19005, 18080,
                19000, 8080-8081, 9090-9091
```

## Configuration Files

### envoy-bootstrap.yaml
Static Envoy configuration that:
- Listens on port 10000 (mapped to 18080 on host)
- Requests dynamic xDS configuration from flexds at `flexds:18000`
- Supports HTTP/1.1 and HTTP/2 (CodecType: AUTO)
- Includes admin interface on port 19000 (19000 on host)
- Configured for STRICT_DNS cluster discovery

### compose.yaml
Orchestrates all services with:
- Consul cluster: 3 servers for HA, 1 client agent for registration
- flexds: Watches Consul, pushes config to Envoy
- Envoy: Receives config from flexds, proxies traffic
- REST services: 2 FastAPI instances with auto-registration
- gRPC services: 2 Node.js instances with auto-registration
- Service dependencies and health checks
- Shared network (`flexds-net`)

## Common Tasks

### Add a New Service

1. **Create service directory** (e.g., `new-service/`):
   ```bash
   mkdir container/new-service
   cd container/new-service
   touch Containerfile app.py  # or appropriate files
   ```

2. **Add to compose.yaml**:
   ```yaml
   new-service:
     build:
       context: ./new-service
       dockerfile: Containerfile
     container_name: new-service
     ports:
       - "9999:9999"
     networks:
       - flexds-net
     environment:
       - CONSUL_HOST=consul-agent
       - CONSUL_PORT=8500
       - SERVICE_NAME=new-service
       - SERVICE_ID=new-service:9999
     depends_on:
       - consul-agent
   ```

3. **Implement self-registration** in service startup:
   - Register with Consul using environment variables
   - Include routing metadata if needed
   - Register on startup, deregister on shutdown

### View Registered Services

```bash
# Via Consul API
curl http://localhost:8500/v1/catalog/services

# Via Consul UI
# Access http://localhost:8500/ui/ in browser

# Check specific service
curl http://localhost:8500/v1/catalog/service/hello-service
curl http://localhost:8500/v1/catalog/service/grpc-service
```

### Modify Envoy Configuration

Edit `envoy-bootstrap.yaml` to change static routes or listeners, then restart Envoy:
```bash
podman compose restart envoy
```

For dynamic configuration changes, services automatically update through flexds:
1. Service registers with new metadata in Consul
2. flexds watches Consul and detects change
3. flexds generates new XDS snapshot
4. Envoy receives update via ADS stream
5. Envoy applies new configuration (no restart needed)

### View flexds Metrics

```bash
curl http://localhost:19005/metrics
```

Look for metrics related to:
- XDS snapshots and updates
- Consul watch activity (discovery changes)
- gRPC connections (services connecting to ADS)

### Debug Service Connectivity

```bash
# Test DNS from flexds container
podman exec flexds nslookup consul-agent
podman exec flexds nslookup rest-service-1
podman exec flexds nslookup grpc-service-1

# Test from REST service
podman exec rest-service-1 curl http://consul-agent:8500/v1/status/leader

# Test from gRPC service
podman exec grpc-service-1 curl http://consul-agent:8500/v1/catalog/services
```

### Test Load Balancing

```bash
# REST service load balancing
for i in {1..10}; do curl http://localhost:18080/hello-service; done

# Check which instance responded by looking at logs
podman compose logs rest-service-1 | grep "Request"
podman compose logs rest-service-2 | grep "Request"

# gRPC load balancing
for i in {1..10}; do \
  grpcurl -plaintext -H "X-Service: grpc-service" \
    localhost:18080 grpc_streaming.StreamingService/Health
done
```

## Troubleshooting

### Services Won't Start (Exit Code 1-2)

1. Check logs:
   ```bash
   podman compose logs --tail=100
   ```

2. Verify image availability:
   ```bash
   podman images
   ```

3. Check network:
   ```bash
   podman network ls
   podman network inspect flexds-net
   ```

### Services Fail to Register with Consul

Check that:
- Consul agent is running: `podman compose logs consul-agent`
- Services can reach Consul: `podman exec rest-service-1 curl http://consul-agent:8500/v1/status/leader`
- Service environment variables are correct: `podman compose config | grep -A 10 rest-service-1`

### Envoy Can't Reach flexds

- Ensure `envoy-bootstrap.yaml` specifies `flexds:18000` as ADS cluster
- Verify both services are on same network: `podman network inspect flexds-net`
- Check flexds logs for gRPC server startup: `podman compose logs flexds | grep "ADS"`
- Check Envoy logs for connection errors: `podman compose logs envoy | grep "upstream"`

### REST Service Returns 502 (Bad Gateway) Through Envoy

- Check that service is registered in Consul: `curl http://localhost:18500/v1/catalog/service/hello-service`
- Check Envoy cluster health: `curl http://localhost:19000/clusters | grep hello-service`
- Verify service is actually running: `curl http://localhost:8080/health`
- Check Envoy logs for routing errors: `podman compose logs envoy`

### gRPC Service Doesn't Respond via Envoy

- Verify HTTP/2 support: `curl http://localhost:19000/clusters | grep http2`
- Check service registration includes `http2: true` metadata: `curl http://localhost:18500/v1/catalog/service/grpc-service`
- Test direct access first: `grpcurl -plaintext localhost:9090 list`
- Check Envoy logs: `podman compose logs envoy | grep grpc`

### Slow or Stuck Services

- Check Consul cluster quorum: `curl http://localhost:8500/v1/status/leader` should return immediately
- Monitor container resource usage: `podman stats`
- Check for lock contention in logs: `podman compose logs | grep -i "lock\|wait"`

## Next Steps

1. **Test the services**: Run curl/grpcurl commands from "Testing Services" section
2. **Explore Consul UI**: Visit http://localhost:8500/ui to see registered services
3. **Monitor Envoy**: Watch `http://localhost:19000/stats` as requests flow through
4. **Add custom services**: Create new services in subdirectories with self-registration
5. **Explore flexds metrics**: Check `http://localhost:19005/metrics` to see discovery activity
6. **Test load balancing**: Run requests in a loop and watch logs to verify distribution

## Architecture Notes

### Why STRICT_DNS?
STRICT_DNS clusters resolve hostnames once per refresh interval and load-balance across resolved addresses. This is perfect for container networks where service discovery is dynamic and we want hostname resolution (not IP-based endpoints).

### Why Metadata-Driven Configuration?
Services register their own routing rules in Consul metadata. This allows:
- Centralized service ownership of routing logic
- Dynamic route updates without Envoy restarts
- Clear coupling between service and its routes

### Why Service Self-Registration?
Rather than external shell scripts:
- Services control their own lifecycle
- No race conditions between service startup and registration
- Built-in deregistration on graceful shutdown
- Environment variables provide flexible configuration

## References

- [Envoy Proxy Documentation](https://www.envoyproxy.io/docs)
- [flexds Repository](https://github.com/moonkev/flexds)
- [Consul Service Catalog](https://www.consul.io/docs/services)
- [FastAPI Documentation](https://fastapi.tiangolo.com/)
- [Node.js gRPC Documentation](https://grpc.io/docs/languages/node/)
- [grpcurl Usage](https://github.com/fullstorydev/grpcurl)
- [OCI Image Spec](https://github.com/opencontainers/image-spec)

