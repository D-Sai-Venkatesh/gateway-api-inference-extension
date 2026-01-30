# Workload Registry Method Refactoring

## Date: 2026-01-30

## Problem Identified

The original implementation exposed `GetWorkloadRegistry()` which returned the internal `WorkloadRegistry` object. While the methods `IncrementActive()` and `DecrementActive()` were called correctly on the registry itself (not on a copy), this design had several issues:

1. **Poor Encapsulation:** Callers had direct access to internal registry implementation
2. **Inconsistent API:** Other datastore operations (Pool, Objective, Pod) don't expose internal structures
3. **Unclear Intent:** Method names `IncrementActive`/`DecrementActive` don't clearly convey request lifecycle

## Solution

Moved workload tracking methods to the Datastore interface with clearer naming:

### New Datastore Interface Methods

```go
// Workload operations
WorkloadHandleNewRequest(workloadID string)
WorkloadHandleCompletedRequest(workloadID string)
WorkloadGetRequestRate(workloadID string) float64
WorkloadGetMetrics(workloadID string) *WorkloadMetrics
GetWorkloadRegistry() *WorkloadRegistry  // Kept for flow control plugins
```

### Implementation in datastore.go

```go
func (ds *datastore) WorkloadHandleNewRequest(workloadID string) {
    ds.workloadRegistry.IncrementActive(workloadID)
}

func (ds *datastore) WorkloadHandleCompletedRequest(workloadID string) {
    ds.workloadRegistry.DecrementActive(workloadID)
}

func (ds *datastore) WorkloadGetRequestRate(workloadID string) float64 {
    return ds.workloadRegistry.GetRequestRate(workloadID)
}

func (ds *datastore) WorkloadGetMetrics(workloadID string) *WorkloadMetrics {
    return ds.workloadRegistry.GetMetrics(workloadID)
}
```

### Updated Callers

**Before:**
```go
s.datastore.GetWorkloadRegistry().IncrementActive(workloadID)
s.datastore.GetWorkloadRegistry().DecrementActive(workloadID)
```

**After:**
```go
s.datastore.WorkloadHandleNewRequest(workloadID)
s.datastore.WorkloadHandleCompletedRequest(workloadID)
```

## Benefits

1. **Better Encapsulation:** WorkloadRegistry is now an implementation detail
2. **Clearer Intent:** Method names explicitly describe request lifecycle events
3. **Consistent API:** Matches pattern of Pool/Objective/Pod operations
4. **Easier Testing:** Can mock Datastore without knowing about WorkloadRegistry
5. **Future Flexibility:** Can change internal implementation without breaking callers

## Files Modified

- [`pkg/epp/datastore/datastore.go`](pkg/epp/datastore/datastore.go) - Added new interface methods and implementations
- [`pkg/epp/handlers/server.go`](pkg/epp/handlers/server.go) - Updated callers to use new methods

## Backward Compatibility

`GetWorkloadRegistry()` is retained in the interface for:
- Flow control plugins that need direct registry access for priority scoring
- Internal use within the datastore package

This maintains compatibility while providing a cleaner public API.

## Testing

All builds pass:
```bash
go build ./pkg/epp/...
go build ./...
```

## Next Steps

Consider further refactoring to eliminate `GetWorkloadRegistry()` entirely by:
1. Passing request rate as metadata to flow control
2. Using dependency injection for comparator construction
3. Making WorkloadRegistry completely private

---

**Reviewed by:** User feedback on 2026-01-30
**Status:** ✅ Complete