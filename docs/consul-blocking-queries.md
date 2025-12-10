# Consul Blocking Queries

## Overview

Consul blocking queries are a mechanism for efficient service discovery polling. Rather than continuously polling Consul for changes, the client establishes a long-lived connection that Consul holds open until either a change occurs or a timeout is reached.

## How It Works

### Basic Flow

1. **Client makes a blocking query** with:
   - `WaitIndex` - The last change index the client has seen
   - `WaitTime` - Maximum time to wait for changes (e.g., 2 seconds)

2. **Consul holds the connection open** and watches for changes to the service catalog

3. **Two possible outcomes:**
   - **Change occurs** → Consul **immediately returns** the new data (doesn't wait the full timeout)
   - **No change before timeout** → Consul **returns empty response** after the wait time expires

## Timeline Examples

### Scenario 1: Change Occurs Within Timeout (2 seconds)

```
Time 0:00.0s   → Client calls client.Catalog().Services() with WaitTime: 2s
Time 0:00.0s   → Consul receives blocking query, starts watching
Time 0:00.0s   → Client connection established, waiting...
Time 0:00.5s   → Service registers/deregisters in Consul
Time 0:00.5s   → Consul IMMEDIATELY returns new service list to client
               → CLIENT PROCESSES CHANGE RIGHT AWAY
Time 0:00.5s   → Next blocking query starts (waiting for the next change)
```

**Result:** Service changes detected in ~500ms (immediate upon change)

### Scenario 2: No Changes Before Timeout

```
Time 0:00.0s   → Client calls client.Catalog().Services() with WaitTime: 2s
Time 0:00.0s   → Consul receives blocking query, starts watching
Time 0:00.0s   → Client connection established, waiting...
Time 0:02.0s   → No changes occurred, timeout reached
Time 0:02.0s   → Consul returns "no changes" response
               → Client sees meta.LastIndex == lastIndex
               → Client continues to next iteration
Time 0:02.0s   → Next blocking query starts immediately
```

**Result:** Service discovery loop continues with ~2 second intervals when stable

## Implementation in flexds

### Code Structure

```go
func WatchConsulBlocking(ctx context.Context, client *consulapi.Client, cache cachev3.SnapshotCache, cfg *Config) {
    var lastIndex uint64
    
    for {
        // Create query options with 2-second wait time
        queryOpts := &consulapi.QueryOptions{
            WaitIndex: lastIndex,        // Last change index we saw
            WaitTime:  time.Duration(cfg.WaitTimeSec) * time.Second,  // 2 seconds
        }
        queryOpts = queryOpts.WithContext(ctx)
        
        // This call blocks until change occurs OR 2 seconds pass
        services, meta, err := client.Catalog().Services(queryOpts)
        
        // If LastIndex unchanged, Consul timed out with no changes
        if meta.LastIndex == lastIndex {
            continue  // Loop and query again
        }
        
        // LastIndex changed - new services discovered
        lastIndex = meta.LastIndex
        // Process the service list and push to Envoy
        xds.BuildAndPushSnapshot(...)
    }
}
```

### Current Configuration

- **WaitTime:** 2 seconds (`WaitTimeSec: 2` in `main.go`)
- **Why 2 seconds?**
  - Short enough for responsive shutdown (max 2s wait before context cancellation takes effect)
  - Long enough to reduce CPU usage from tight polling
  - Standard practice for production service discovery systems

## Key Properties

### Real-Time Updates
✅ Service changes are **detected immediately** when they occur, not batched or delayed  
✅ Updates are **not queued** - you get the next change instantly

### Efficient Resource Usage
✅ No wasteful polling in tight loops  
✅ Connection held open minimizes network round trips  
✅ Consul CPU usage reduced compared to frequent polling

### Graceful Shutdown
✅ With 2-second timeout, application can shutdown cleanly within 2-5 seconds  
✅ Context cancellation (`WithContext(ctx)`) allows interrupting mid-wait  
✅ No hanging processes waiting for long blocking queries

## Consul API Behavior

### When a change occurs:
- Consul detects the change (service register/deregister/health update)
- Consul finds all waiting clients watching the changed resource
- Consul immediately sends response with new `LastIndex` to all clients
- Clients receive response and can act on the change

### When no change occurs:
- After `WaitTime` expires, Consul sends empty response
- Response includes same `LastIndex` as request
- Client's check `if meta.LastIndex == lastIndex { continue }` catches this
- No processing needed, loop restarts immediately with new blocking query

## Trade-offs

| Aspect | With 2s Timeout | With 10s Timeout |
|--------|-----------------|-----------------|
| Shutdown Time | ~2 seconds | ~10 seconds |
| Change Detection | Immediate + 0-2s safety check | Immediate + 0-10s safety check |
| Network Overhead | Slight (10% more requests if stable) | Lower |
| Responsiveness | High | Medium |

**Verdict:** 2-second timeout is optimal for this use case, providing real-time service discovery with fast, clean shutdowns.

## Related Consul Concepts

- **Index-Based Queries:** The `WaitIndex` parameter uses Consul's internal version numbering, ensuring you never miss changes between queries
- **Atomic Updates:** All service catalog changes increment the same `LastIndex`, so you process updates atomically
- **Blocking Query Semantics:** Guaranteed to return immediately if index advances, never loses updates due to timing
