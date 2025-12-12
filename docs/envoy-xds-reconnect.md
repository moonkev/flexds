# Envoy xDS gRPC Stream Not Reconnecting – Troubleshooting Guide

When Envoy’s xDS gRPC stream fails to reconnect after the control plane has been down for some time, several factors may be responsible. This guide provides a full overview of why this happens and how to force Envoy to reconnect without restarting the process.

---

## 1. Why Envoy May Not Reconnect Automatically

Envoy normally retries xDS gRPC connections indefinitely with exponential backoff. However, certain conditions can stop or severely delay reconnection.

### 1.1 Backoff Timer at Maximum
Envoy uses exponential backoff for gRPC failures:
- Initial retry ~1s  
- Exponential increase  
- Maximum backoff: **30–120 seconds**

If the xDS server comes back online while Envoy is still in backoff sleep, reconnection will be delayed.

Check logs for:
```
GrpcStream retrying with backoff
```

---

### 1.2 Envoy Marked the xDS Cluster as Permanently Failed
Envoy may consider the xDS cluster unhealthy if:
- DNS resolves to 0 endpoints  
- IP address changed but Envoy cached old value  
- TLS handshake fails  
- STRICT_DNS cluster had an empty DNS response  

In this state, Envoy may not retry until the cluster becomes healthy again.

---

### 1.3 The xDS Cluster Has No Hosts
If the cluster representing your xDS server has **no endpoints**, Envoy cannot reconnect.

Check:
```bash
curl localhost:15000/clusters
```

Look for:
```
hosts: []
healthy: 0
```

---

## 2. Forcing Envoy to Reconnect Without Restarting

Below are safe methods to force reconnection.

---

### 2.1 Trigger DNS Re-Resolution

If your xDS cluster uses `STRICT_DNS` or `LOGICAL_DNS`:

```bash
curl -X POST http://localhost:15000/dns_refresh
```

Or force cluster-wide re-evaluation:

```bash
curl -X POST http://localhost:15000/healthcheck/fail
sleep 1
curl -X POST http://localhost:15000/healthcheck/ok
```

---

### 2.2 Trigger Cluster Reload
For ADS or gRPC-driven clusters:

```bash
curl -X POST http://localhost:15000/config_dump
```

This indirectly refreshes xDS cluster connections.

---

### 2.3 Hot-Reload Bootstrap to Recreate Static Clusters

If the xDS cluster is part of the bootstrap:

```bash
touch /etc/envoy/envoy.yaml
curl -X POST localhost:15000/hot_restart_version
```

This forces Envoy to re-read static cluster definitions.

---

### 2.4 Force HTTP/2 Connection Drops (Most Effective)

If the gRPC stream is stuck (common after long outages):

```bash
curl -X POST localhost:15000/close_connections
```

This drops all upstream connections—including the xDS connection—forcing immediate reconnect.

---

### 2.5 Trigger a No-Op Push From XDS Server

Any update from the xDS server causes reconnect:
- CDS update  
- LDS update  
- RDS update  
- EDS update  

Even a no-op config triggers reconnection.

---

## 3. Recommended Debugging Steps

1. Inspect the xDS cluster:
   ```bash
   curl localhost:15000/clusters | grep -A30 xds
   ```

2. Check logs:
   ```
   [warning][config] GrpcStream connection failure
   ```

3. Force reconnect:
   ```bash
   curl -X POST localhost:15000/close_connections
   ```

---

## 4. Summary

Envoy should reconnect automatically, but reconnection can stop or delay due to:
- Empty DNS results  
- Unhealthy xDS cluster  
- Stale IP/TLS state  
- Exhausted backoff timer  
- Stuck HTTP/2 streams  

You can force reconnection via:
- `dns_refresh`
- `close_connections`
- Cluster reloads
- xDS no-op pushes
- Bootstrap timestamp updates

This markdown serves as a reference for diagnosing xDS reconnect issues.
