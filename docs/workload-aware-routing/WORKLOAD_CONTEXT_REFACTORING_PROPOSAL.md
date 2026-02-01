# Workload Context Refactoring Proposal

## Problem Statement

Currently, the workload-aware routing implementation has architectural complexity:

1. **WorkloadRegistry Injection Complexity**: The `WorkloadRegistry` must be injected into flow control plugins during initialization, causing:
   - Complex factory patterns for plugin creation
   - Special handling for dynamic priority band creation
   - Tight coupling between datastore and flow control layers

2. **Metadata Duplication**: Workload context is copied from `RequestContext.WorkloadContext` to `Request.Metadata` map just to pass it through the flow control layer to the `WorkloadAwarePolicy`.

3. **Current Data Flow**:
   ```
   Headers → RequestContext.WorkloadContext → Request.Metadata (copy) 
   → FlowControlRequest.GetMetadata() → WorkloadAwarePolicy.computeScore()
   ```

## Proposed Solution

**Pass WorkloadContext directly through the FlowControlRequest interface**, eliminating the need for WorkloadRegistry injection in plugins.

### Architecture Changes

#### 1. Extend FlowControlRequest Interface

**File**: `pkg/epp/flowcontrol/types/request.go`

Add method to `FlowControlRequest` interface:
```go
// GetWorkloadContext returns the workload identity and priority information
// for workload-aware routing. Returns nil if no workload context is available.
GetWorkloadContext() *datastore.WorkloadContext
```

**Rationale**: 
- Makes workload context a first-class citizen in flow control
- Eliminates metadata map abuse
- Type-safe access to workload information

#### 2. Update flowControlRequest Implementation

**File**: `pkg/epp/requestcontrol/admission.go`

```go
type flowControlRequest struct {
    requestID         string
    fairnessID        string
    priority          int
    requestByteSize   uint64
    reqMetadata       map[string]any
    inferencePoolName string
    modelName         string
    targetModelName   string
    workloadContext   *datastore.WorkloadContext  // NEW FIELD
}

func (r *flowControlRequest) GetWorkloadContext() *datastore.WorkloadContext {
    return r.workloadContext
}
```

Modify `Admit()` method:
```go
func (fcac *FlowControlAdmissionController) Admit(
    ctx context.Context,
    reqCtx *handlers.RequestContext,
    priority int,
) error {
    // ... existing code ...
    
    fcReq := &flowControlRequest{
        requestID:         reqCtx.SchedulingRequest.RequestId,
        fairnessID:        reqCtx.FairnessID,
        priority:          priority,
        requestByteSize:   uint64(reqCtx.RequestSize),
        reqMetadata:       reqCtx.Request.Metadata,
        inferencePoolName: fcac.poolName,
        modelName:         reqCtx.IncomingModelName,
        targetModelName:   reqCtx.TargetModelName,
        workloadContext:   reqCtx.WorkloadContext,  // PASS DIRECTLY
    }
    
    // ... rest of code ...
}
```

#### 3. Remove Metadata Copy from Request Handler

**File**: `pkg/epp/handlers/request.go`

**REMOVE** lines 97-103:
```go
// DELETE THIS BLOCK:
// Copy workload context into metadata for flow control layer
if reqCtx.WorkloadContext != nil {
    reqCtx.Request.Metadata["workload_id"] = reqCtx.WorkloadContext.WorkloadID
    reqCtx.Request.Metadata["criticality"] = reqCtx.WorkloadContext.Criticality
}
```

#### 4. Update WorkloadAwarePolicy to Use Direct Access

**File**: `pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go`

```go
func (p *WorkloadAwarePolicy) computeScore(item types.QueueItemAccessor, now time.Time) float64 {
    // Get workload context directly from request
    workloadCtx := item.OriginalRequest().GetWorkloadContext()
    
    // Default values if no workload context
    workloadID := "default"
    criticality := 3
    
    if workloadCtx != nil {
        workloadID = workloadCtx.WorkloadID
        criticality = workloadCtx.Criticality
    }
    
    // Validate criticality range
    if criticality < 1 || criticality > 5 {
        criticality = 3
    }
    
    // Get workload metrics from registry
    avgWaitTime := 0.0
    requestRate := 0.0
    if p.workloadRegistry != nil {
        metrics := p.workloadRegistry.GetMetrics(workloadID)
        if metrics != nil {
            avgWaitTime = metrics.AverageWaitTime.Seconds()
        }
        requestRate = p.workloadRegistry.GetRequestRate(workloadID)
    }
    
    // ... rest of scoring logic unchanged ...
}
```

#### 5. Remove WorkloadRegistry from Plugin

**Option A: Keep Registry for Metrics (RECOMMENDED)**
- Keep `workloadRegistry` field in `WorkloadAwarePolicy`
- Keep `SetWorkloadRegistry()` method
- Only use registry for **reading metrics**, not for tracking requests
- Simpler migration path

**Option B: Complete Removal (FUTURE ENHANCEMENT)**
- Remove `workloadRegistry` field entirely
- Pass metrics through `FlowControlRequest` interface
- Requires more extensive changes to datastore integration

**Recommendation**: Start with Option A for this refactoring phase.

## Benefits

### 1. **Simplified Plugin Initialization**
- No need for `WorkloadAwarePolicyFactory`
- No special handling for dynamic priority bands
- Standard plugin registration works out of the box

### 2. **Cleaner Data Flow**
```
Headers → RequestContext.WorkloadContext → FlowControlRequest.GetWorkloadContext()
→ WorkloadAwarePolicy.computeScore()
```

### 3. **Type Safety**
- No more `interface{}` casting from metadata map
- Compile-time type checking
- Clear API contract

### 4. **Reduced Coupling**
- Flow control layer doesn't need datastore dependency for context
- Workload context is just data, not behavior
- Registry only needed for metrics aggregation

### 5. **Better Testability**
- Mock `FlowControlRequest` with test workload contexts
- No need to inject registry into every test
- Isolated unit tests for scoring logic

## Migration Impact Analysis

### Files to Modify
1. ✅ `pkg/epp/flowcontrol/types/request.go` - Add interface method
2. ✅ `pkg/epp/requestcontrol/admission.go` - Implement method, pass context
3. ✅ `pkg/epp/handlers/request.go` - Remove metadata copy
4. ✅ `pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go` - Use direct access

### Files to Review (No Changes Needed)
- `pkg/epp/datastore/workload_registry.go` - No changes
- `pkg/epp/handlers/server.go` - No changes (still tracks lifecycle)
- `pkg/epp/datastore/datastore.go` - No changes

### Backward Compatibility
- ✅ No breaking changes to public APIs
- ✅ Existing tests continue to work
- ✅ Metadata map still available for other uses
- ✅ Registry injection still works (just not required for context)

## Potential Issues & Mitigations

### Issue 1: Import Cycle Risk
**Problem**: `flowcontrol/types` importing `datastore` could create cycle

**Mitigation**: 
- Define `WorkloadContext` in a shared package (e.g., `pkg/epp/types`)
- OR: Keep in `datastore` package (flow control already imports it indirectly)
- **Recommended**: Keep in `datastore` - no new dependencies needed

### Issue 2: Nil WorkloadContext Handling
**Problem**: Need to handle nil contexts gracefully

**Mitigation**:
- Already handled in current implementation
- Default values applied when nil
- No regression risk

### Issue 3: Testing Complexity
**Problem**: Need to update test mocks

**Mitigation**:
- Add `GetWorkloadContext()` to mock implementations
- Return nil for tests that don't need workload awareness
- Minimal test changes required

## Implementation Plan

### Phase 1: Add Interface Method (Low Risk)
1. Add `GetWorkloadContext()` to `FlowControlRequest` interface
2. Implement in `flowControlRequest` struct
3. Return nil initially (no behavior change)
4. **Verify**: Build succeeds, tests pass

### Phase 2: Pass Context Through (Medium Risk)
1. Modify `Admit()` to set `workloadContext` field
2. Update `WorkloadAwarePolicy.computeScore()` to use new method
3. Keep metadata copy temporarily (parallel path)
4. **Verify**: Both paths work, metrics match

### Phase 3: Remove Metadata Copy (Low Risk)
1. Remove metadata copy from `request.go`
2. Remove metadata access from `computeScore()`
3. **Verify**: Tests pass, behavior unchanged

### Phase 4: Cleanup (Optional)
1. Consider removing registry injection (future work)
2. Document new pattern for other plugins
3. Update developer guide

## Recommendation

**PROCEED with this refactoring** because:

1. ✅ **Cleaner Architecture**: Eliminates unnecessary complexity
2. ✅ **Type Safety**: Compile-time guarantees vs runtime casting
3. ✅ **Low Risk**: Incremental changes with verification at each step
4. ✅ **Better Maintainability**: Clearer data flow, easier to understand
5. ✅ **No Breaking Changes**: Backward compatible migration path

The current metadata-based approach was a workaround. This refactoring makes workload context a proper first-class concept in the flow control layer.

## Alternative Considered: Keep Current Approach

**Pros**:
- No code changes needed
- Works today

**Cons**:
- Metadata map abuse (type-unsafe)
- Complex plugin initialization
- Tight coupling
- Harder to test
- Confusing for new developers

**Verdict**: Refactoring is worth the effort for long-term maintainability.