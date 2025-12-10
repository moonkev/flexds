# FlexDS

An Envoy XDS (Extensible Discovery Service) server that bridges Consul service discovery with Envoy proxy, enabling dynamic configuration, flexible routing, and automatic service mesh management.

## Quick Start

### Prerequisites
- Go 1.24+
- Consul running (default: `localhost:8500`)
- Envoy proxy with bootstrap configuration

### Build & Run

```bash
go build
./consul-xds-gateway-routing -consul consul.example.com:8500
```

The XDS server will start on **port 18000** and begin streaming configuration to Envoy.

## Architecture

```
┌─────────────┐
│   Consul    │ ← Service Registry (blocking queries)
└──────┬──────┘
       ▼
┌────────────────────────────┐
│  FlexDS                    │ ← This service (port 18000 gRPC)
│  Dynamic Configuration     │
└──────┬─────────────────────┘
       │ ADS Protocol Stream
       ▼
┌────────────────────────────┐
│  Envoy Proxy               │ ← port 18080 HTTP listener
│  Configuration             │
└──────┬─────────────────────┘
       │ HTTP Requests
       ▼
┌────────────────────────────┐
│  Backend Services          │
│  (py-web, nomad, etc.)     │
└────────────────────────────┘
```

## Features

### 🔄 Dynamic Service Discovery
- Real-time watching of Consul service catalog
- Blocking queries (10s) for efficient updates
- Automatic health filtering (only healthy instances)
- Zero-downtime service registration/deregistration

### 🎯 Flexible Multi-Route Routing
Support multiple independent routes per service with three matching strategies:

- **Path-Based** - Route by URL path prefix
- **Header-Based** - Route by HTTP header values
- **Combined** - Require both path AND header match

Each route supports independent path rewriting, making it simple to handle multiple API versions or access patterns from a single service.

### 📡 XDS Protocol Implementation
- **Type**: State-of-the-World (SOTW) with versioning
- **Services**: LDS (Listeners), CDS (Clusters), EDS (Endpoints), RDS (Routes)
- **Discovery Mode**: ADS (Aggregated Discovery Service)
- **gRPC**: Keepalive 30s, timeout 5s, 1M concurrent streams

### 🛡️ Robust Error Handling
- Automatic DNS resolution (hostname → IP conversion)
- Service port validation before configuration
- Graceful shutdown with query cancellation
- Comprehensive logging with `[TAG]` markers

## Configuration

### Port Mapping

| Port  | Purpose                | Protocol | Notes |
|-------|------------------------|----------|-------|
| 18000 | XDS gRPC Server        | gRPC     | Envoy connects here |
| 18080 | HTTP Listener          | HTTP     | Receives requests from clients |
| 19005 | Admin/Metrics          | HTTP     | `/metrics` and `/config_dump` |

### Environment

**Default values** (override with flags):
```bash
-consul string          Consul address (default "127.0.0.1:8500")
-ads-port int          XDS server port (default 18000)
-admin-port int        Admin port (default 19005)
```

### Envoy Configuration

See `envoy-bootstrap.yaml`:
- **Node ID**: `ingress-gateway`
- **XDS Server**: `127.0.0.1:18000`
- **Listener Port**: `0.0.0.0:18080`
- **Protocol**: ADS with RDS per-listener

## Multi-Route Routing Guide

Define flexible routing rules in Consul service metadata. No Host headers required—routes automatically match any incoming host.

### Metadata Format

Configure routes in Consul service metadata using this format:

```
route_N_match_type        = "path" | "header" | "both"
route_N_path_prefix       = "/path/to/service"
route_N_header_name       = "X-Header-Name"
route_N_header_value      = "header-value"
route_N_prefix_rewrite    = "/"
route_N_hosts             = "host1.com,host2.com"  (optional)
```

Where `N` is a number (1, 2, 3, ...) for each route. Supports up to 10 routes per service.

**Important**: Consul metadata keys don't support dots—use underscores: `route_1_match_type` ✅ (not `route.1.match_type` ❌)

### Example 1: Simple Path-Based Routing

Register a service that routes by URL path:

```bash
curl -X PUT http://localhost:8500/v1/agent/service/register \
  -H "Content-Type: application/json" \
  -d '{
    "ID": "py-web-1",
    "Name": "py-web",
    "Port": 8080,
    "Address": "192.168.50.15",
    "Meta": {
      "route_1_match_type": "path",
      "route_1_path_prefix": "/api/v1/py-web",
      "route_1_prefix_rewrite": "/"
    }
  }'
```

**Usage**:
```bash
# Request matches path prefix
curl http://127.0.0.1:18080/api/v1/py-web/status
# → Forwards to: http://192.168.50.15:8080/status
```

### Example 2: Header-Based Routing

Route based on HTTP headers instead of paths:

```bash
curl -X PUT http://localhost:8500/v1/agent/service/register \
  -H "Content-Type: application/json" \
  -d '{
    "ID": "nomad-1",
    "Name": "nomad",
    "Port": 4646,
    "Address": "192.168.50.20",
    "Meta": {
      "route_1_match_type": "header",
      "route_1_header_name": "X-Service",
      "route_1_header_value": "nomad",
      "route_1_path_prefix": "/",
      "route_1_prefix_rewrite": "/"
    }
  }'
```

**Usage**:
```bash
# Request with matching header
curl -H "X-Service: nomad" http://127.0.0.1:18080/ui/jobs
# → Forwards to: http://192.168.50.20:4646/ui/jobs

# Request without header (doesn't match this route)
curl http://127.0.0.1:18080/ui/jobs
# → Route not matched, tries next route or returns 404
```

### Example 3: Multiple Routes Per Service

Create **two independent routes** for the same service—requests match if they satisfy **either** route:

```bash
curl -X PUT http://localhost:8500/v1/agent/service/register \
  -H "Content-Type: application/json" \
  -d '{
    "ID": "py-web-1",
    "Name": "py-web",
    "Port": 8080,
    "Address": "192.168.50.15",
    "Meta": {
      "route_1_match_type": "path",
      "route_1_path_prefix": "/api/v1/py-web",
      "route_1_prefix_rewrite": "/",
      
      "route_2_match_type": "header",
      "route_2_header_name": "X-Service",
      "route_2_header_value": "py-web",
      "route_2_path_prefix": "/",
      "route_2_prefix_rewrite": "/"
    }
  }'
```

**Usage options**:
```bash
# Route 1: Path-based (no special headers needed)
curl http://127.0.0.1:18080/api/v1/py-web/status
# → Forwards to: /status

# Route 2: Header-based (any path)
curl -H "X-Service: py-web" http://127.0.0.1:18080/anything
# → Forwards to: /anything
```

### Example 4: Combined Path AND Header Matching

Require **both** path AND header to match:

```bash
curl -X PUT http://localhost:8500/v1/agent/service/register \
  -H "Content-Type: application/json" \
  -d '{
    "ID": "py-web-1",
    "Name": "py-web",
    "Port": 8080,
    "Address": "192.168.50.15",
    "Meta": {
      "route_1_match_type": "both",
      "route_1_path_prefix": "/api/v1/py-web",
      "route_1_header_name": "X-Version",
      "route_1_header_value": "v1",
      "route_1_prefix_rewrite": "/"
    }
  }'
```

**Usage**:
```bash
# ✅ Matches (both conditions met)
curl -H "X-Version: v1" http://127.0.0.1:18080/api/v1/py-web/status
# → Forwards to: /status

# ❌ Does NOT match (missing header)
curl http://127.0.0.1:18080/api/v1/py-web/status
# → Route not matched

# ❌ Does NOT match (wrong path)
curl -H "X-Version: v1" http://127.0.0.1:18080/status
# → Route not matched
```

### Example 5: Complex Multi-Route Setup

A realistic setup with multiple routes for different API versions:

```bash
curl -X PUT http://localhost:8500/v1/agent/service/register \
  -H "Content-Type: application/json" \
  -d '{
    "ID": "py-web-1",
    "Name": "py-web",
    "Port": 8080,
    "Address": "192.168.50.15",
    "Meta": {
      "route_1_match_type": "path",
      "route_1_path_prefix": "/api/v1/py-web",
      "route_1_prefix_rewrite": "/",
      
      "route_2_match_type": "path",
      "route_2_path_prefix": "/api/v2/py-web",
      "route_2_prefix_rewrite": "/v2",
      
      "route_3_match_type": "header",
      "route_3_header_name": "X-Service",
      "route_3_header_value": "py-web",
      "route_3_path_prefix": "/",
      "route_3_prefix_rewrite": "/",
      
      "route_4_match_type": "both",
      "route_4_path_prefix": "/health",
      "route_4_header_name": "X-Internal-Check",
      "route_4_header_value": "true",
      "route_4_prefix_rewrite": "/"
    }
  }'
```

**Usage**:
```bash
# Route 1: Public API v1
curl http://127.0.0.1:18080/api/v1/py-web/status
# → Forwards to: /status

# Route 2: Public API v2 (with prefix rewrite)
curl http://127.0.0.1:18080/api/v2/py-web/data
# → Forwards to: /v2/data

# Route 3: Header-based internal access
curl -H "X-Service: py-web" http://127.0.0.1:18080/admin/users
# → Forwards to: /admin/users

# Route 4: Monitoring (requires both path and header)
curl -H "X-Internal-Check: true" http://127.0.0.1:18080/health
# → Forwards to: /health
```

### Optional: Restrict Routes to Specific Domains

By default, routes match **any Host header** (via wildcard `*`). To restrict to specific domains:

```json
"route_1_hosts": "api.example.com,api-v2.example.com"
```

This restricts the route to only those Host header values. When not specified, routes accept any host.

## Implementation Details

### Single Wildcard Virtual Host Architecture

All routes from all services are consolidated into a single virtual host with wildcard domain. This satisfies Envoy's constraint: "Only a single wildcard domain permitted in route local_route".

```go
vhHost := &route.VirtualHost{
    Name:    "default",
    Domains: []string{"*"},  // Single wildcard for all services
    Routes:  allRoutes,      // All routes merged here
}
```

**Why this design?**
- Simplifies user experience (no need to manage domain mappings)
- Avoids Envoy validation errors
- Routes are evaluated in order—first match wins
- All services accessible via `http://127.0.0.1:18080`

### Dual NodeID Snapshot Storage

Snapshots are stored under both wildcard and specific nodeID to ensure Envoy finds matching configuration:

```go
cache.SetSnapshot(context.Background(), "*", snap)
cache.SetSnapshot(context.Background(), "ingress-gateway", snap)
```

This handles cases where XDS clients use different nodeID values.

### Automatic IP Resolution

When Consul returns hostnames instead of IPs, the system automatically uses the node's IP:

```go
if addr != "" && !isIPAddress(addr) && e.Node.Address != "" {
    addr = e.Node.Address  // Use node IP for hostnames
}
```

**Why?** Prevents Envoy errors like "malformed IP address" when service addresses are DNS names.

## Monitoring & Debugging

### Logging

Service logs include `[TAG]` prefixes for easy filtering:

| Tag | Meaning |
|-----|---------|
| `[SNAPSHOT PUSHED]` | Configuration pushed to Envoy (version, resource counts) |
| `[ENDPOINT]` | Service endpoint discovered/updated |
| `[PARSE ROUTES]` | Route metadata parsed from Consul |
| `[STREAM REQUEST]` | Envoy requesting configuration |
| `[STREAM RESPONSE]` | Service responding with configuration |
| `[STREAM OPEN/CLOSED]` | Connection lifecycle events |

**Filter logs**:
```bash
grep "SNAPSHOT PUSHED" logfile    # See all config pushes
grep "ENDPOINT" logfile           # See discovered endpoints
grep "STREAM" logfile             # See XDS protocol flow
```

### Verify Configuration Delivery

1. **Check Envoy listeners**: `http://localhost:19000/config_dump`
   - Look for single virtual host named `"default"` with wildcard domain `*`
   - Routes should list your services with correct path prefixes

2. **Check endpoints**: `http://localhost:19000/clusters`
   - Should show endpoint IPs and ports
   - Look for `health_flags::healthy` status

3. **Test routing**:
   ```bash
   curl http://127.0.0.1:18080/api/v1/py-web/status
   curl -H "X-Service: py-web" http://127.0.0.1:18080/status
   ```

### Troubleshooting

**Services not appearing in Envoy:**
- Check Consul: `curl http://localhost:8500/v1/catalog/services`
- Verify healthy: `curl http://localhost:8500/v1/health/service/SERVICE_NAME`
- Check XDS logs for `[SNAPSHOT PUSHED]`

**Connection failures to endpoints:**
- Verify endpoint is running: `curl http://<ip>:<port>/`
- Check firewall rules
- Ensure Consul registered port matches actual service port

**Routes not matching:**
- Check metadata in Consul: `curl http://localhost:8500/v1/catalog/service/SERVICE_NAME`
- Verify underscore format: `route_1_match_type` not `route.1.match_type`
- Check Envoy config dump for route configuration
- Review service logs for `[PARSE ROUTES]` output
