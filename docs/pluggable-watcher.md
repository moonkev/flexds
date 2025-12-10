# Pluggable Consul Watcher Architecture

## Overview

The Consul watcher in flexds is pluggable, allowing you to swap different batching/update strategies at runtime. This provides flexibility to tune the system for your specific needs without code changes. Each strategy is implemented in its own file for clarity and maintainability.

## Architecture

```
┌─────────────────────────────────────┐
│     ConsulWatcher Interface         │
│  (defines Watch() method)           │
└────────────┬────────────────────────┘
             │
             ├─→ ImmediateWatcher  (immediate.go)
             ├─→ DebounceWatcher   (debounce.go)
             └─→ BatchWatcher      (batch.go)
             
Each implements ConsulWatcher and can be swapped at runtime
```

## Package Structure

The `watcher` package is organized for maintainability:

```
watcher/
├── interface.go       # ConsulWatcher interface, factory, types
├── immediate.go       # ImmediateWatcher implementation
├── debounce.go        # DebounceWatcher implementation
├── batch.go           # BatchWatcher implementation
└── helpers.go         # Shared utilities (filterServices)
```

### File Purposes

| File | Lines | Purpose |
|------|-------|---------|
| `interface.go` | 41 | Public API, factory, interface definitions |
| `immediate.go` | 64 | Real-time update strategy |
| `debounce.go` | 88 | 500ms debounce batching |
| `batch.go` | 100 | Explicit size/timeout batching |
| `helpers.go` | 12 | Shared utility functions |

## Watcher Strategies

### 1. **Immediate** (default)
**File:** `watcher/immediate.go`

- **Behavior:** Applies updates as soon as they're detected
- **Use case:** Real-time service discovery, low-change environments
- **Latency:** Immediate (+ Consul blocking query wait time)
- **Load:** Higher (more Envoy snapshots generated)
- **Flag:** `-watcher-strategy immediate`
- **Shutdown Time:** 2-3 seconds

```
Change detected → Immediately push to Envoy
Change detected → Immediately push to Envoy
Change detected → Immediately push to Envoy
```

**Flow:**
```
Consul Query ──→ Change? ──YES──→ Call Handler ──→ Push Snapshot
               │              │
               └─NO───────────┴──→ Loop again
```

### 2. **Debounce**
**File:** `watcher/debounce.go`

- **Behavior:** Waits 500ms after detecting a change before applying
- **Use case:** Handling burst changes, reducing snapshot thrashing
- **Latency:** 0-500ms additional delay
- **Load:** Lower (batches rapid changes)
- **Flag:** `-watcher-strategy debounce`
- **Shutdown Time:** 2-3 seconds

```
Change 1 detected → [Start 500ms debounce timer]
Change 2 detected → [Reset timer]
Change 3 detected → [Reset timer]
[500ms quiet]     → Push all 3 changes at once to Envoy
```

**Flow:**
```
Consul Query ──→ Change? ──YES──→ Start/Reset Timer
               │              │
               └─NO───────────┘
                                    │
                                    ▼
                            Timer Fires ──→ Call Handler ──→ Push Snapshot
```

**Implementation Example:**
```go
debounceTimer := time.NewTimer(0)
debounceTimer.Stop()

select {
case <-debounceTimer.C:
    // Apply the update now
    w.cfg.Handler(latestServices)
case <-ctx.Done():
    return nil
default:
    // Query Consul
    // On change: reset timer with debounceInterval
    debounceTimer.Reset(500 * time.Millisecond)
}
```

### 3. **Batch**
**File:** `watcher/batch.go`

- **Behavior:** Applies update when batch reaches size limit OR timeout expires
- **Config:** max 5 changes per batch, timeout after 1 second
- **Use case:** Very high-change environments, reducing Envoy churn
- **Latency:** 0-1s additional delay
- **Load:** Lowest (explicitly batches changes)
- **Flag:** `-watcher-strategy batch`
- **Shutdown Time:** 2-3 seconds

```
Change 1 detected → [Start 1s timer, batch count = 1]
Change 2 detected → [batch count = 2]
Change 3 detected → [batch count = 3]
Change 4 detected → [batch count = 4]
Change 5 detected → [batch count = 5] → Push all 5 at once to Envoy
                                         [Reset timer, batch count = 0]
```

**Flow:**
```
Consul Query ──→ Change? ──YES──→ Increment Count
               │              │
               └─NO───────────┴──→ Check Count ──┐
                                                  │
                   Reached Limit? ─YES─→ Call Handler ──→ Push Snapshot
                          │
                          NO
                          │
                    Start Timer if not running
                          │
                    Timer Fires ──→ Call Handler ──→ Push Snapshot
```

## Usage

### Command-Line Usage

```bash
# Default: immediate updates
./flexds -consul localhost:8500

# Debounce rapid changes (500ms window)
./flexds -consul localhost:8500 -watcher-strategy debounce

# Batch up to 5 changes or 1 second timeout
./flexds -consul localhost:8500 -watcher-strategy batch
```

### Recommended Configurations

#### Development / Testing
```bash
./flexds -watcher-strategy immediate
```
- Real-time feedback
- Easy to debug
- See every change immediately

#### Production - Stable Environment (< 1 change/sec)
```bash
./flexds -watcher-strategy immediate
```
- No unnecessary batching
- Responsive to service changes
- Low change volume = low Envoy load

#### Production - Dynamic Environment (> 1 change/sec)
```bash
./flexds -watcher-strategy debounce
```
- Batches micro-bursts (e.g., service restart with health updates)
- Very responsive (500ms latency)
- Reduces Envoy snapshot thrashing

#### Production - Highly Dynamic (chaos testing, rolling deployments)
```bash
./flexds -watcher-strategy batch
```
- Explicit batching limits
- Most predictable load on Envoy
- Up to 1s latency

## Implementation Details

### ConsulWatcher Interface

All watchers implement this interface:

```go
type ConsulWatcher interface {
    // Watch starts watching Consul for service changes
    // It blocks until context is cancelled
    Watch(ctx context.Context) error
}
```

### WatcherConfig

Configuration struct passed to all watchers:

```go
type WatcherConfig struct {
    Client      *consulapi.Client        // Consul client
    Cache       cachev3.SnapshotCache    // XDS snapshot cache
    WaitTimeSec int                      // Consul blocking query wait time
    Handler     ServiceChangeHandler     // Called when update should apply
}
```

### ServiceChangeHandler

Callback invoked when the watcher decides to apply updates:

```go
type ServiceChangeHandler func(services []string) error
```

Example handler in `consul.go`:

```go
handler := func(services []string) error {
    log.Printf("[CONSUL HANDLER] processing %d services: %v", len(services), services)
    metricServicesDiscovered.Set(float64(len(services)))
    xds.BuildAndPushSnapshot(cache, client, services, "*", &routeBuilder{}, metricSnapshotsPushed)
    return nil
}
```

### Factory Function

```go
func NewWatcher(strategy string, cfg *WatcherConfig) ConsulWatcher {
    switch strategy {
    case "debounce":
        return NewDebounceWatcher(cfg, 500*time.Millisecond)
    case "batch":
        return NewBatchWatcher(cfg, 5, 1*time.Second)
    case "immediate":
        fallthrough
    default:
        return NewImmediateWatcher(cfg)
    }
}
```

## File Details

### `interface.go` (41 lines)
- **Purpose:** Defines the public API and watcher contract
- **Contents:**
  - `ConsulWatcher` interface
  - `ServiceChangeHandler` type
  - `WatcherConfig` struct
  - `NewWatcher()` factory function
- **Exports:** Everything (uppercase)

### `immediate.go` (64 lines)
- **Purpose:** Real-time update strategy
- **Contents:**
  - `ImmediateWatcher` struct
  - `NewImmediateWatcher()` constructor
  - `Watch()` method
- **Logic:** Query Consul, on change immediately call handler

### `debounce.go` (88 lines)
- **Purpose:** Batches rapid changes with debounce timer
- **Contents:**
  - `DebounceWatcher` struct
  - `NewDebounceWatcher()` constructor
  - `Watch()` method with timer management
- **Logic:** Query Consul, on change start/reset 500ms timer, apply when timer fires

### `batch.go` (100 lines)
- **Purpose:** Explicit change batching
- **Contents:**
  - `BatchWatcher` struct
  - `NewBatchWatcher()` constructor
  - `Watch()` method with batch counting
- **Logic:** Query Consul, on change increment counter, apply when count reaches 5 or 1s timer fires

### `helpers.go` (12 lines)
- **Purpose:** Shared utility functions
- **Contents:**
  - `filterServices()` - extract service names, exclude "consul"
- **Used by:** All watchers

## Adding a New Watcher Strategy

To add a new strategy (e.g., "adaptive"):

**1. Create a new file `watcher/adaptive.go`:**

```go
package watcher

import (
    "context"
    "log"
    "time"

    consulapi "github.com/hashicorp/consul/api"
)

// AdaptiveWatcher adjusts batching based on change rate
type AdaptiveWatcher struct {
    cfg *WatcherConfig
    // Your custom fields
}

// NewAdaptiveWatcher creates a new adaptive watcher
func NewAdaptiveWatcher(cfg *WatcherConfig) *AdaptiveWatcher {
    return &AdaptiveWatcher{cfg: cfg}
}

// Watch implements the ConsulWatcher interface
func (w *AdaptiveWatcher) Watch(ctx context.Context) error {
    var lastIndex uint64
    // Your implementation
    // Call w.cfg.Handler(services) when ready to apply changes
    return nil
}
```

**2. Register it in `interface.go`:**

```go
func NewWatcher(strategy string, cfg *WatcherConfig) ConsulWatcher {
    switch strategy {
    case "adaptive":
        return NewAdaptiveWatcher(cfg)
    case "debounce":
        return NewDebounceWatcher(cfg, 500*time.Millisecond)
    // ... existing cases
    }
}
```

That's it! No other files need modification.

## Implementation Template

Each watcher file follows this pattern:

```go
package watcher

import (
    "context"
    "log"
    "time"

    consulapi "github.com/hashicorp/consul/api"
)

// [Strategy]Watcher [description]
type [Strategy]Watcher struct {
    cfg *WatcherConfig
    // Strategy-specific fields
}

// New[Strategy]Watcher creates a new [strategy] watcher
func New[Strategy]Watcher(cfg *WatcherConfig, ...) *[Strategy]Watcher {
    return &[Strategy]Watcher{
        cfg: cfg,
        // Initialize fields
    }
}

// Watch implements the ConsulWatcher interface
func (w *[Strategy]Watcher) Watch(ctx context.Context) error {
    var lastIndex uint64
    
    for {
        select {
        case <-ctx.Done():
            log.Printf("[WATCHER:%s] stopping, context cancelled", "STRATEGY")
            return nil
        default:
        }
        
        // Query Consul
        queryOpts := &consulapi.QueryOptions{
            WaitIndex: lastIndex,
            WaitTime:  time.Duration(w.cfg.WaitTimeSec) * time.Second,
        }
        queryOpts = queryOpts.WithContext(ctx)
        
        services, meta, err := w.cfg.Client.Catalog().Services(queryOpts)
        if err != nil {
            if ctx.Err() != nil {
                return nil
            }
            log.Printf("[WATCHER:%s] error: %v", "STRATEGY", err)
            time.Sleep(1 * time.Second)
            continue
        }
        
        if meta.LastIndex == lastIndex {
            continue  // No changes
        }
        
        lastIndex = meta.LastIndex
        svcList := filterServices(services)
        
        // Your batching/timing logic here
        // Call w.cfg.Handler(svcList) when ready
    }
}
```

## Testing Individual Watchers

Each watcher can be tested in isolation:

```go
func TestImmediateWatcher(t *testing.T) {
    cfg := &watcher.WatcherConfig{
        Client:      mockClient,
        Cache:       mockCache,
        WaitTimeSec: 2,
        Handler: func(services []string) error {
            // Your test logic
            return nil
        },
    }
    w := watcher.NewImmediateWatcher(cfg)
    
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    err := w.Watch(ctx)
    // Assert behavior
}
```

## Performance Characteristics

| Aspect | Immediate | Debounce | Batch |
|--------|-----------|----------|-------|
| Snapshots/min | 60 (per change) | 15-30 (batched) | 5-10 (explicit) |
| Shutdown Time | 2-3s | 2-3s | 2-3s |
| Envoy Churn | High | Medium | Low |
| Latency | Immediate | 0-500ms | 0-1s |
| CPU Usage | Higher | Medium | Lower |
| Best For | Dev, stable | Dynamic | Very dynamic |

*Estimates based on 1-60 changes/minute range*

## Logging

Each watcher logs with its strategy prefix:

- `[WATCHER:IMMEDIATE]` - Immediate watcher events
- `[WATCHER:DEBOUNCE]` - Debounce watcher events
- `[WATCHER:BATCH]` - Batch watcher events
- `[CONSUL HANDLER]` - Handler invocation

Example logs:
```
[WATCHER:DEBOUNCE] detected change: lastIndex=100 newIndex=101
[WATCHER:DEBOUNCE] starting debounce timer (500ms)
[WATCHER:DEBOUNCE] resetting debounce timer, more changes coming
[WATCHER:DEBOUNCE] debounce timer fired, applying batched update with 3 services
[CONSUL HANDLER] processing 3 services: [py-web nomad vault]
```

Filter logs by strategy:
```bash
grep WATCHER:DEBOUNCE logfile
grep CONSUL logfile
```

## Package Dependencies

Each watcher file imports only what it needs:

- `context` - for context handling
- `log` - for logging
- `time` - for timers and durations
- `github.com/hashicorp/consul/api` - Consul client library

## Future Extensions

The pluggable architecture makes it easy to add:

- **Weighted Batching** - Apply after 3 "important" changes or 10 "minor" ones
- **Adaptive Batching** - Automatically adjust batch size based on change rate
- **Priority-Based** - Different strategies for different service types
- **Health Check Integration** - Batch based on health status vs metadata changes
- **Metrics-Driven** - Switch strategies based on Envoy configuration push latency
- **Circuit Breaker** - Skip updates if Envoy is overwhelmed
- **Rate Limiting** - Limit updates to N per second

## Summary

The pluggable watcher architecture provides:

✅ **Flexibility** - Swap strategies at runtime via command-line flag  
✅ **Modularity** - Each strategy in its own file (~60-100 lines)  
✅ **Extensibility** - Add new strategies with one new file + one line  
✅ **Testability** - Test each watcher in isolation  
✅ **Maintainability** - Clear separation of concerns  
✅ **Production Ready** - Choose optimal strategy per environment  

No code changes needed - just change the `-watcher-strategy` flag!
