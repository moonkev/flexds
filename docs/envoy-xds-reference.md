
# Envoy Resources and xDS APIs – Control Plane Reference

This document is a **single, comprehensive reference** for Envoy resources and their
corresponding **xDS APIs**, intended for engineers building a **custom Envoy control plane**
(e.g. backed by Consul).

It covers:
- All Envoy resource types
- Core and auxiliary xDS APIs
- State-of-the-World vs Delta xDS internals
- A practical **Consul → xDS mapping table**
- Control-plane design guidance

---

## 1. Envoy Configuration Model (High Level)

Envoy is configured dynamically through a set of **typed resources** delivered by a control
plane over **xDS (Discovery Service) APIs** using gRPC.

```
State Backend (Consul, KV, etc)
            ↓
     Control Plane Cache
            ↓
        xDS (gRPC)
            ↓
          Envoy
```

Each resource:
- Is independently versioned
- Is validated by Envoy (ACK/NACK)
- Can be delivered as a full snapshot or incrementally

---

## 2. Core xDS APIs

| API | Name | Purpose |
|----|----|----|
| LDS | Listener Discovery Service | Inbound listeners and filter chains |
| RDS | Route Discovery Service | HTTP routing configuration |
| CDS | Cluster Discovery Service | Logical upstream services |
| EDS | Endpoint Discovery Service | Concrete service endpoints |

These APIs define the **data plane contract** between Envoy and the control plane.

---

## 3. Listener Discovery Service (LDS)

### Resource
```
envoy.config.listener.v3.Listener
```

### Responsibility
Defines **how traffic enters Envoy**.

- IP / port bindings
- Transport protocol (TCP, UDP)
- TLS configuration
- Filter chains
- SNI matching

### Relationships
```
Listener
 └── FilterChain
      └── HttpConnectionManager
           └── RDS
```

### Characteristics
- Low churn
- High blast radius if misconfigured
- Often Git- or policy-driven

---

## 4. Route Discovery Service (RDS)

### Resource
```
envoy.config.route.v3.RouteConfiguration
```

### Responsibility
Defines **how HTTP traffic is routed**.

- Virtual hosts
- Path / header matching
- Traffic splitting
- Retries, timeouts
- Redirects

### Characteristics
- Moderate churn
- Safe to update frequently
- Decoupled from listeners

---

## 5. Cluster Discovery Service (CDS)

### Resource
```
envoy.config.cluster.v3.Cluster
```

### Responsibility
Defines **logical upstream services**.

- Load balancing policy
- Circuit breakers
- Connection pools
- TLS to upstreams
- Outlier detection

### Relationships
```
Route → Cluster → EDS
```

### Characteristics
- Low churn
- Structural changes
- Often service-lifecycle driven

---

## 6. Endpoint Discovery Service (EDS)

### Resource
```
envoy.config.endpoint.v3.ClusterLoadAssignment
```

### Responsibility
Defines **actual backend instances**.

- IP + port
- Health
- Locality
- Weights

### Characteristics
- High churn
- Performance critical
- Must be incremental-friendly

---

## 7. Auxiliary xDS APIs

### 7.1 SDS – Secret Discovery Service

**Resource**
```
envoy.extensions.transport_sockets.tls.v3.Secret
```

Used for:
- TLS certificates
- Private keys
- CA bundles
- mTLS rotation

---

### 7.2 ECDS – Extension Config Discovery Service

**Resource**
```
envoy.config.core.v3.TypedExtensionConfig
```

Used for:
- WASM filters
- Lua filters
- Dynamic filter configuration

---

### 7.3 RTDS – Runtime Discovery Service

**Resource**
```
envoy.service.runtime.v3.Runtime
```

Used for:
- Feature flags
- Runtime knobs
- Gradual rollouts

---

### 7.4 ADS – Aggregated Discovery Service

ADS multiplexes **all xDS APIs over a single gRPC stream**.

**Recommendation:** Always use ADS unless you have a specific reason not to.

---

## 8. State-of-the-World (SotW) xDS

### Model
- Each update contains the **entire set of resources**
- Envoy replaces its local state atomically

### Pros
- Simple
- Easy to reason about
- Fewer edge cases

### Cons
- More bandwidth
- Less efficient at scale

### Best For
- Small to medium deployments
- Early control-plane development

---

## 9. Delta xDS Internals

Delta xDS delivers **incremental changes only**.

### Key Concepts

#### Resource Versions
Each resource has:
- `name`
- `version`
- `resource payload`

#### Envoy Tracks
- Subscribed resources
- Last acknowledged version per resource

---

### Delta Update Flow

1. Envoy subscribes to resources
2. Control plane sends:
   - Added resources
   - Updated resources
   - Removed resource names
3. Envoy validates:
   - ACK → applies changes
   - NACK → rejects delta

---

### Delta xDS Message Types

- `resources`
- `removed_resources`
- `system_version_info`

### Control Plane Responsibilities
- Track per-Envoy subscriptions
- Track per-resource versions
- Maintain last-known-good state

### When to Use Delta
- Large fleets
- High EDS churn
- Many independent services

---

## 10. Consul → xDS Mapping Table

| Consul Source | Consul API | xDS Resource | Notes |
|--------------|-----------|-------------|------|
| Services | `/v1/catalog/services` | CDS | One cluster per service |
| Service instances | `/v1/health/service/<name>` | EDS | Passing checks only |
| Service metadata | Service tags/meta | CDS / RDS | Policy inputs |
| KV routes | `/v1/kv/*` | RDS | HTTP routing |
| KV listeners | `/v1/kv/*` | LDS | Optional |
| TLS material | External (Vault) | SDS | Not Consul-native |
| Feature flags | KV | RTDS | Optional |

**Important:** Consul indexes are per-query; track them independently.

---

## 11. Watching Strategy with Consul

### Correct Pattern

```
Consul
  ↓ (blocking query)
Single Watcher per Resource
  ↓
Control Plane Cache
  ↓
Many Envoys
```

### Avoid
- One Consul watch per Envoy
- Polling
- Event API for state

---

## 12. Versioning and ACK/NACK

Every xDS response includes:
- `version_info`
- `nonce`

Rules:
- Never reuse version strings
- Always retain last-good config
- Log and observe NACKs
- Do not roll back automatically

A common pattern:
```
version_info = "consul-index:<n>"
```

---

## 13. Recommended Ownership Model

| Resource | Owner |
|-------|------|
| LDS | Static / Git |
| RDS | KV / GitOps |
| CDS | Service catalog |
| EDS | Service discovery |
| SDS | Secrets manager |
| RTDS | Feature system |

---

## 14. Summary

Envoy’s xDS APIs form a **strongly typed, versioned control-plane contract**.

Key principles:
- Separate structure (LDS/CDS) from churn (EDS/RDS)
- Centralize state, fan out updates
- Prefer ADS
- Use Delta xDS at scale
- Treat Consul as a state backend, not a control plane

This model scales from laptops to multi-datacenter meshes.

---
