# Container Services

This directory contains the Docker/Podman compose setup and test services for the flexds xDS control plane.

## Directory Structure

```
container/
├── compose.yaml                 # Main compose file (orchestrates all services)
├── Dockerfile                   # flexds binary build (Go)
├── Containerfile                # Alternative container file (Podman)
├── envoy-bootstrap.yaml         # Envoy proxy configuration
├── rest-service/                # Python Flask REST service
│   ├── app.py
│   ├── Dockerfile
│   └── README.md
├── grpc-service/                # Node.js gRPC streaming service
│   ├── streaming.proto
│   ├── server.js
│   ├── package.json
│   ├── Dockerfile
│   └── README.md
└── README.md                    # This file
```

## Services

### flexds
- **Description**: xDS control plane (Envoy Data Source) — serves Consul data to Envoy proxies via gRPC
- **Ports**: 
  - `18000/tcp` — ADS (Aggregated Discovery Service) gRPC
  - `19005/tcp` — Admin HTTP (metrics, health)
- **Network**: `flexds-net`
- **Build**: From repo root Dockerfile, built for arm64

### Consul
- **Description**: Service registry and configuration store
- **Ports**: `8500/tcp` — HTTP API
- **Network**: `flexds-net`
- **Image**: `consul:1.15.4` (arm64)

### Envoy
- **Description**: Service proxy (routes requests through configured listeners)
- **Ports**:
  - `18080/tcp` — Example proxy listener (configured via bootstrap.yaml)
  - `19000/tcp` — Admin console
- **Network**: `flexds-net`
- **Image**: `envoyproxy/envoy:distroless-v1.33.13` (arm64)
- **Config**: Mounted from `envoy-bootstrap.yaml`

### rest-service
- **Description**: Simple Flask REST API for testing HTTP proxying
- **Ports**: `8080/tcp` — HTTP API
- **Network**: `flexds-net`
- **Endpoints**:
  - `GET /health` — Health check
  - `GET /api/v1/hello` — Hello message
  - `GET /api/v1/info` — Service info

### grpc-service
- **Description**: Node.js gRPC service with streaming RPC methods
- **Ports**: `9090/tcp` — gRPC API
- **Network**: `flexds-net`
- **Endpoints**:
  - `Health()` — Unary RPC health check
  - `CounterStream(start, count)` — Server-streaming RPC returning integers

## Quick Start

### Build and Run from Container Directory

```bash
# From container/ directory
podman compose up --build -d

# View logs
podman compose logs -f

# Bring services down
podman compose down
```

### Run from Repository Root

```bash
cd /path/to/flexds
podman compose -f container/compose.yaml up --build -d
```

## Testing Services

### REST Service

#### Direct Access (no proxy)

```bash
# Health check
curl http://localhost:8080/health

# Hello endpoint
curl http://localhost:8080/api/v1/hello

# Service info
curl http://localhost:8080/api/v1/info
```

#### Via Envoy Proxy (port 18080)

```bash
# Requires Envoy listener configured to route to rest-service
# See envoy-bootstrap.yaml for listener configuration

curl http://localhost:18080/api/v1/hello
curl http://localhost:18080/api/v1/info
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
```

#### Via Envoy Proxy (port 18080)

```bash
# Requires Envoy listener configured for gRPC routing
# For now, Envoy is configured for HTTP/1.1; gRPC requires HTTP/2 listener

# Once Envoy is configured for gRPC:
grpcurl -plaintext localhost:18080 list
grpcurl -plaintext localhost:18080 grpc_streaming.StreamingService/Health
```

## Checking Service Status

### View Running Containers

```bash
podman compose ps
```

### Inspect Individual Service Logs

```bash
podman compose logs flexds
podman compose logs consul
podman compose logs envoy
podman compose logs rest-service
podman compose logs grpc-service
```

### Health Checks

```bash
# Consul health
curl http://localhost:8500/v1/status/leader

# flexds admin/metrics
curl http://localhost:19005/healthz
curl http://localhost:19005/metrics

# REST service health
curl http://localhost:8080/health

# gRPC service health (via grpcurl)
grpcurl -plaintext localhost:9090 grpc_streaming.StreamingService/Health
```

## Envoy Admin Console

Access Envoy's admin console at: **http://localhost:19000**

Useful endpoints:
- `http://localhost:19000/stats` — Service statistics
- `http://localhost:19000/clusters` — Configured clusters
- `http://localhost:19000/listeners` — Configured listeners
- `http://localhost:19000/config_dump` — Full Envoy configuration

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        flexds-net (bridge)                      │
│                                                                 │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐       │
│  │  Consul  │  │  flexds  │  │  Envoy   │  │   REST   │       │
│  │ :8500    │  │:18000    │  │ :18080   │  │ :8080    │       │
│  │          │  │:19005    │  │ :19000   │  │          │       │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘       │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                    gRPC Service                          │  │
│  │                       :9090                              │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                            │
                Host Ports Exposed:
                8500, 18000, 19005, 18080, 19000, 8080, 9090
```

## Configuration Files

### envoy-bootstrap.yaml
Static Envoy configuration that:
- Listens on port 18080 for HTTP/1.1 traffic
- Requests xDS configuration from flexds at `flexds:18000`
- Includes admin interface on port 19000

To add routes to the REST service or gRPC service, register them in Consul and configure flexds to populate them, or edit the bootstrap file directly.

### compose.yaml
Orchestrates all services with:
- Service definitions (image, build, ports, networks)
- Volume mounts (envoy-bootstrap.yaml)
- Health checks (Consul, REST service)
- Shared network (`flexds-net`)

## Common Tasks

### Add a New Upstream Service

1. **Via Compose** (easiest for testing):
   - Add a new service block in `compose.yaml`
   - Ensure it's on the `flexds-net` network
   - Expose its port

2. **Via Consul** (production approach):
   - Register the service in Consul's catalog
   - Configure flexds to watch Consul
   - flexds populates Envoy with cluster/endpoint info

### Modify Envoy Configuration

Edit `envoy-bootstrap.yaml` to:
- Add new listeners (ports)
- Change routing rules
- Configure load balancing policies

Then restart Envoy:
```bash
podman compose restart envoy
```

### View flexds Metrics

```bash
curl http://localhost:19005/metrics
```

Look for metrics related to:
- gRPC streams (opened, closed, requests)
- Consul watch activity
- Snapshot updates

### Debug DNS Resolution Inside Containers

```bash
# Test DNS from flexds container
podman exec flexds nslookup consul
podman exec flexds nslookup rest-service

# Test from REST service
podman exec rest-service nslookup grpc-service
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
   podman network inspect flexds_flexds-net
   ```

### Envoy Can't Reach flexds

- Ensure `envoy-bootstrap.yaml` has correct `flexds` address (service name, not IP)
- Verify both services are on the same network: `podman network inspect flexds_flexds-net`
- Check flexds logs for gRPC errors: `podman compose logs flexds`

### REST Service Returns 502 (Bad Gateway) Through Envoy

- Envoy's example listener may not be configured to route to rest-service
- Update `envoy-bootstrap.yaml` to add a cluster and route for the REST service
- Or test via direct port 8080 first

### gRPC Service Doesn't Respond via Envoy

- Envoy's current bootstrap is HTTP/1.1; gRPC requires HTTP/2
- Update the listener in `envoy-bootstrap.yaml` to enable HTTP/2 protocol
- Or test via direct port 9090 first with grpcurl

## Next Steps

1. **Test the services**: Run curl/grpcurl commands from "Testing Services" section
2. **Extend Envoy configuration**: Add routes to rest-service and gRPC service in `envoy-bootstrap.yaml`
3. **Add custom services**: Create new services in subdirectories and add to `compose.yaml`
4. **Explore flexds logs**: Check how flexds watches Consul and pushes updates to Envoy
5. **Monitor Envoy**: Watch Envoy's admin console as configurations change

## References

- [Envoy Proxy Documentation](https://www.envoyproxy.io/docs)
- [flexds Repository](https://github.com/moonkev/flexds)
- [Consul Service Catalog](https://www.consul.io/docs/connect/proxies/envoy)
- [gRPC Documentation](https://grpc.io/docs)
- [grpcurl Usage](https://github.com/fullstorydev/grpcurl)
