# Average Wait Time Implementation Plan

## Problem Statement

Current priority formula uses individual request wait time (computed at queue entry, always ~0):
```
Priority = (WaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
```

**Issue:** `WaitTime` is meaningless since it's computed when request enters queue.

## Solution: Workload Average Wait Time (EMA)

Replace individual request wait time with **workload's historical average wait time** using Exponential Moving Average (EMA).

### New Formula
```
Priority = (AvgWaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
```

Where `AvgWaitTime` = Workload's historical average time from queue entry to dispatch

---

## Implementation Details

### 1. Data Structure Changes

**File:** `pkg/epp/datastore/workload_registry.go`

Add to `WorkloadMetrics`:
```go
type WorkloadMetrics struct {
    WorkloadID            string
    TotalRequests         int64
    ActiveRequests        int64
    SlidingWindowRequests int64
    WindowStartTime       time.Time
    LastRequestTime       time.Time
    
    // NEW: Average wait time tracking
    AverageWaitTime       time.Duration  // EMA of wait times
    DispatchedCount       int64          // Total requests dispatched
    EMAAlpha              float64        // Decay factor (default: 0.2)
    
    mu                    sync.RWMutex
}
```

### 2. New Method: RecordDispatch

**File:** `pkg/epp/datastore/workload_registry.go`

```go
// RecordDispatch updates the average wait time when a request is dispatched.
// Uses Exponential Moving Average (EMA) for smooth, adaptive tracking.
//
// Formula: AvgWaitTime = α × CurrentWait + (1-α) × PreviousAvg
// Where α (alpha) controls sensitivity to recent changes (default: 0.2)
func (wr *WorkloadRegistry) RecordDispatch(workloadID string, waitTime time.Duration) {
    value, ok := wr.workloads.Load(workloadID)
    if !ok {
        return // Workload not found, nothing to update
    }
    
    metrics := value.(*WorkloadMetrics)
    metrics.mu.Lock()
    defer metrics.mu.Unlock()
    
    // Initialize alpha if not set
    if metrics.EMAAlpha == 0 {
        metrics.EMAAlpha = 0.2 // Default: 20% current, 80% history
    }
    
    // First dispatch: initialize average
    if metrics.DispatchedCount == 0 {
        metrics.AverageWaitTime = waitTime
    } else {
        // EMA calculation
        alpha := metrics.EMAAlpha
        oldAvg := float64(metrics.AverageWaitTime)
        newWait := float64(waitTime)
        metrics.AverageWaitTime = time.Duration(alpha*newWait + (1-alpha)*oldAvg)
    }
    
    metrics.DispatchedCount++
}
```

### 3. Datastore Interface Addition

**File:** `pkg/epp/datastore/datastore.go`

Add to `Datastore` interface:
```go
// Workload operations
WorkloadHandleNewRequest(workloadID string)
WorkloadHandleCompletedRequest(workloadID string)
WorkloadHandleDispatchedRequest(workloadID string, waitTime time.Duration)  // NEW
WorkloadGetRequestRate(workloadID string) float64
WorkloadGetMetrics(workloadID string) *WorkloadMetrics
GetWorkloadRegistry() *WorkloadRegistry
```

Implementation in `datastore` struct:
```go
func (ds *datastore) WorkloadHandleDispatchedRequest(workloadID string, waitTime time.Duration) {
    ds.workloadRegistry.RecordDispatch(workloadID, waitTime)
}
```

### 4. Call Site: Dispatch Point

**File:** `pkg/epp/flowcontrol/controller/internal/processor.go:355`

**Current code:**
```go
func (sp *ShardProcessor) dispatchItem(itemAcc types.QueueItemAccessor) error {
    req := itemAcc.OriginalRequest()
    // ... existing code ...
    
    removedItem := removedItemAcc.(*FlowItem)
    sp.logger.V(logutil.TRACE).Info("Item dispatched.", "flowKey", req.FlowKey(), "reqID", req.ID())
    removedItem.FinalizeWithOutcome(types.QueueOutcomeDispatched, nil)
    return nil
}
```

**Modified code:**
```go
func (sp *ShardProcessor) dispatchItem(itemAcc types.QueueItemAccessor) error {
    req := itemAcc.OriginalRequest()
    // ... existing code ...
    
    removedItem := removedItemAcc.(*FlowItem)
    
    // NEW: Record dispatch time for workload tracking
    waitTime := sp.clock.Since(itemAcc.EnqueueTime())
    if workloadCtx, ok := req.GetMetadata()["workload_context"].(*datastore.WorkloadContext); ok {
        sp.datastore.WorkloadHandleDispatchedRequest(workloadCtx.WorkloadID, waitTime)
    }
    
    sp.logger.V(logutil.TRACE).Info("Item dispatched.", 
        "flowKey", req.FlowKey(), "reqID", req.ID(), "waitTime", waitTime)
    removedItem.FinalizeWithOutcome(types.QueueOutcomeDispatched, nil)
    return nil
}
```

**Requirements:**
- `ShardProcessor` needs access to `Datastore` (check if already available)
- `EnqueueTime()` is available via `QueueItemAccessor` interface
- `clock` is available in `ShardProcessor` for time calculations

### 5. Priority Score Calculation Update

**File:** `pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go:139`

**Current code:**
```go
func (c *WorkloadAwareComparator) computeScore(item types.QueueItemAccessor, now time.Time) float64 {
    // Extract workload context
    metadata := item.OriginalRequest().GetMetadata()
    workloadCtx, ok := metadata["workload_context"].(*datastore.WorkloadContext)
    if !ok || workloadCtx == nil {
        return 0.0
    }
    
    // Get request rate from registry
    requestRate := c.workloadRegistry.GetRequestRate(workloadCtx.WorkloadID)
    
    // Compute wait time for THIS request
    waitTime := now.Sub(item.EnqueueTime()).Seconds()
    
    // Normalize components
    normalizedWait := math.Min(waitTime/60.0, 1.0)
    normalizedCrit := float64(workloadCtx.Criticality) / 5.0
    normalizedRate := math.Min(requestRate/100.0, 1.0)
    
    // Compute score
    return (normalizedWait * c.weights.WaitTimeWeight) +
           (normalizedCrit * c.weights.CriticalityWeight) -
           (normalizedRate * c.weights.RequestRateWeight)
}
```

**Modified code:**
```go
func (c *WorkloadAwareComparator) computeScore(item types.QueueItemAccessor, now time.Time) float64 {
    // Extract workload context
    metadata := item.OriginalRequest().GetMetadata()
    workloadCtx, ok := metadata["workload_context"].(*datastore.WorkloadContext)
    if !ok || workloadCtx == nil {
        return 0.0
    }
    
    // Get workload metrics from registry
    metrics := c.workloadRegistry.GetMetrics(workloadCtx.WorkloadID)
    if metrics == nil {
        return 0.0
    }
    
    // Use workload's AVERAGE wait time instead of individual request wait time
    avgWaitTime := metrics.AverageWaitTime.Seconds()
    requestRate := c.workloadRegistry.GetRequestRate(workloadCtx.WorkloadID)
    
    // Normalize components
    normalizedWait := math.Min(avgWaitTime/60.0, 1.0)  // Cap at 60 seconds
    normalizedCrit := float64(workloadCtx.Criticality) / 5.0
    normalizedRate := math.Min(requestRate/100.0, 1.0)  // Cap at 100 req/s
    
    // Compute score
    return (normalizedWait * c.weights.WaitTimeWeight) +
           (normalizedCrit * c.weights.CriticalityWeight) -
           (normalizedRate * c.weights.RequestRateWeight)
}
```

---

## Data Flow

```
1. Request enters queue
   └─> WorkloadHandleNewRequest() called
       └─> Increments ActiveRequests, updates sliding window

2. Request selected for dispatch by WorkloadAwareComparator
   └─> computeScore() uses workload's AverageWaitTime
       └─> Higher average wait time → Higher priority

3. Request dispatched by ShardProcessor
   └─> dispatchItem() calculates actual wait time
       └─> WorkloadHandleDispatchedRequest(workloadID, waitTime)
           └─> RecordDispatch() updates EMA
               └─> AverageWaitTime = α × waitTime + (1-α) × oldAvg

4. Request completes
   └─> WorkloadHandleCompletedRequest() called
       └─> Decrements ActiveRequests
```

---

## Benefits

1. **Fairness:** Workloads with historically long waits get priority boost
2. **Starvation Prevention:** Low-priority workloads accumulate average wait time
3. **Adaptive:** Recent behavior influences more than old data (via α)
4. **Efficient:** O(1) update, no history storage needed
5. **Self-Correcting:** EMA naturally adapts to changing conditions

---

## Tuning Parameters

### EMA Alpha (α)
- **α = 0.2** (default): 80% history, 20% current - Smooth, stable
- **α = 0.5**: Equal weight - Balanced responsiveness
- **α = 0.1**: 90% history - Very smooth, slow to adapt

**Recommendation:** Start with α=0.2, tune based on testing

### Normalization Caps
- **Wait Time:** 60 seconds (current)
- **Request Rate:** 100 req/s (current)
- **Criticality:** 5 (fixed range 1-5)

---

## Future Enhancement: Percentile-Based (P95)

**Alternative approach for robustness to outliers:**

```go
type WorkloadMetrics struct {
    // ... existing fields
    WaitTimeHistogram *prometheus.Histogram  // Track distribution
}

func (wr *WorkloadRegistry) GetP95WaitTime(workloadID string) time.Duration {
    // Query histogram for 95th percentile
    // More robust to spikes but requires histogram storage
}
```

**Pros:**
- Handles outliers better
- More accurate representation of "typical" wait time
- Industry standard for SLA tracking

**Cons:**
- Requires histogram/sketch data structure
- More memory overhead
- More complex implementation

**Status:** Documented for future exploration, not implementing now

---

## Testing Plan

### Unit Tests
1. `TestRecordDispatch_FirstDispatch` - Initialize average correctly
2. `TestRecordDispatch_EMA` - Verify EMA calculation
3. `TestRecordDispatch_Concurrent` - Thread safety
4. `TestComputeScore_UsesAvgWaitTime` - Priority calculation

### Integration Tests
1. Workload with high average wait time gets priority boost
2. EMA adapts to changing wait times
3. Multiple workloads with different histories

### E2E Tests
1. Send requests with varying wait times
2. Verify workloads with longer average waits dispatch first
3. Verify starvation prevention

---

## Implementation Checklist

- [ ] Add `AverageWaitTime`, `DispatchedCount`, `EMAAlpha` to `WorkloadMetrics`
- [ ] Implement `RecordDispatch()` in `WorkloadRegistry`
- [ ] Add `WorkloadHandleDispatchedRequest()` to `Datastore` interface
- [ ] Implement `WorkloadHandleDispatchedRequest()` in `datastore` struct
- [ ] Verify `ShardProcessor` has access to `Datastore`
- [ ] Modify `dispatchItem()` to call `WorkloadHandleDispatchedRequest()`
- [ ] Update `computeScore()` to use `AverageWaitTime` instead of individual wait time
- [ ] Update documentation and comments
- [ ] Write unit tests
- [ ] Write integration tests
- [ ] Run E2E tests
- [ ] Performance benchmarks

---

**Status:** Ready for implementation
**Estimated Effort:** 4-6 hours
**Priority:** High - Core functionality improvement