# Phase 3: Registry Integration - Implementation Summary

## Decision: Both Handlers in server.go

### Rationale
- **Simplicity**: All tracking logic in one place
- **Reliability**: Defer guarantees cleanup
- **Complete coverage**: Tracks all requests from entry to exit

### Implementation

**Location**: `pkg/epp/handlers/server.go`

**Increment**: Line ~247 (BEFORE `director.HandleRequest()`)
```go
// Increment workload active request count
// Note: This tracks all requests including those rejected by admission control,
// which prevents workloads from gaming the system with invalid requests.
if reqCtx.WorkloadContext != nil {
    s.datastore.GetWorkloadRegistry().IncrementActive(reqCtx.WorkloadContext.WorkloadID)
}
```

**Decrement**: Line ~180 (in defer cleanup)
```go
// Decrement workload active request count on completion/error/disconnect
if reqCtx.WorkloadContext != nil {
    s.datastore.GetWorkloadRegistry().DecrementActive(reqCtx.WorkloadContext.WorkloadID)
}
```

### What "Active" Means
- **Tracks**: Queue time + Processing time + Response streaming
- **Includes**: Rejected requests (briefly, until defer cleanup)
- **Purpose**: Request rate fairness across workloads

### Completed Work
✅ Updated `handlers.Datastore` interface - added `GetWorkloadRegistry()`
✅ Updated `requestcontrol.Datastore` interface - added `GetWorkloadRegistry()`
✅ Fixed `mockDatastore` in tests - added `GetWorkloadRegistry()` method
✅ All tests compile successfully

### Remaining Work
- Add IncrementActive() call in server.go (line ~247)
- Add DecrementActive() call in server.go defer (line ~180)
- Run tests to verify
- Commit changes