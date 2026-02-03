# Phase 7: Workload-Aware Routing Testing - Final Execution Plan

## Critical Understanding: Queue Saturation Strategy

### The Challenge
**Key Insight:** If the vLLM sim pod is not saturated, requests will be dispatched immediately in arrival order, **not** in priority order. The workload-aware prioritization only takes effect when requests are **queued** waiting for capacity.

### Solution: Force Queue Buildup
To properly test workload-aware routing, we must **intentionally saturate** the endpoint so requests queue up and priority ordering can take effect.

**Strategy:**
1. **Pre-saturate** the endpoint with long-running requests
2. **Then** send test requests with different priorities
3. **Observe** that queued requests are dispatched in priority order (not arrival order)

---

## Revised Test Approach

### Test Scenario 1: Basic Priority Ordering (REVISED)

**Objective:** Verify high-criticality requests are dispatched before low-criticality when queued

**Test Steps:**
```
1. Send 5 "blocker" requests (long-running, low priority)
   - These saturate the endpoint and force subsequent requests to queue
   
2. Wait for blockers to start processing (endpoint now saturated)

3. Send test requests in this order:
   - Request A: workload_id=low, criticality=1
   - Request B: workload_id=medium, criticality=3  
   - Request C: workload_id=high, criticality=5
   
4. All 3 requests will queue (endpoint saturated)

5. As blockers complete, queued requests dispatch in priority order
```

**Expected Dispatch Order:** C → B → A (priority order, NOT arrival order)

**Verification:**
- Check `[WA-ROUTING] Request dispatched` logs
- Verify C dispatches first, then B, then A
- Confirm dispatch order differs from arrival order (A, B, C)

---

### Test Scenario 2: Wait Time Boost (REVISED)

**Objective:** Verify older queued requests get priority boost

**Test Steps:**
```
1. Send 5 blocker requests to saturate endpoint

2. Send Request A: workload_id=waiting, criticality=2
   - This queues immediately

3. Wait 30 seconds (Request A accumulates wait time)

4. Send Request B: workload_id=new, criticality=4
   - This also queues

5. As blockers complete, observe dispatch order
```

**Expected Behavior:**
- Request A's wait time boost may overcome B's higher criticality
- Priority formula: (AvgWaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
- After 30s wait, A might have higher score than B

**Verification:**
- Check priority scores in logs
- Verify wait time component increases for Request A
- Confirm dispatch order reflects combined score

---

### Test Scenario 3: Request Rate Fairness (REVISED)

**Objective:** Verify high-rate workloads are penalized when queued

**Test Steps:**
```
1. Send 5 blocker requests to saturate endpoint

2. Rapidly send 10 requests: workload_id=flood, criticality=4
   - All queue, flood workload has high request rate

3. Send 1 request: workload_id=fair, criticality=4
   - Also queues, but fair workload has low request rate

4. As blockers complete, observe dispatch order
```

**Expected Behavior:**
- `flood` workload penalized for high request rate
- `fair` request should dispatch before some `flood` requests
- Despite same criticality, rate penalty affects priority

**Verification:**
- Check request rate metrics in logs
- Verify `fair` request gets priority
- Confirm rate penalty is applied to `flood` workload

---

## Implementation Plan

### Phase 7.1: Add Logging with Unique Marker

**Unique Marker:** `[WA-ROUTING]`

**Files to Modify:**

#### 1. `pkg/epp/handlers/server.go`
```go
// In HandleRequestHeaders(), after workload context extraction
if reqCtx.WorkloadContext != nil {
    logger.Info("[WA-ROUTING] Workload context extracted",
        "request_id", reqCtx.RequestID,
        "workload_id", reqCtx.WorkloadContext.WorkloadID,
        "criticality", reqCtx.WorkloadContext.Criticality)
}
```

#### 2. `pkg/epp/datastore/workload_registry.go`
```go
// In IncrementActive()
logger.V(2).Info("[WA-ROUTING] Workload active incremented",
    "workload_id", workloadID,
    "active_requests", metrics.ActiveRequests,
    "total_requests", metrics.TotalRequests,
    "request_rate", wr.GetRequestRate(workloadID))

// In DecrementActive()
logger.V(2).Info("[WA-ROUTING] Workload active decremented",
    "workload_id", workloadID,
    "active_requests", metrics.ActiveRequests)
```

#### 3. `pkg/epp/requestcontrol/admission.go`
```go
// In Admit(), after creating flowControlRequest
if workloadCtx != nil {
    logger.Info("[WA-ROUTING] Request admitted to flow control",
        "request_id", reqCtx.RequestID,
        "workload_id", workloadCtx.workloadID,
        "criticality", workloadCtx.criticality)
}
```

#### 4. `pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go`
```go
// In computeScore(), after calculating score
logger.V(2).Info("[WA-ROUTING] Priority score computed",
    "workload_id", workloadID,
    "criticality", criticality,
    "avg_wait_time_sec", avgWaitTime,
    "request_rate", requestRate,
    "normalized_wait", normalizedWait,
    "normalized_crit", normalizedCrit,
    "normalized_rate", normalizedRate,
    "final_score", score)
```

#### 5. `pkg/epp/flowcontrol/controller/internal/processor.go`
```go
// When dispatching request (need to find exact location)
logger.Info("[WA-ROUTING] Request dispatched",
    "request_id", item.RequestID(),
    "workload_id", workloadID,
    "queue_size_before", queueSize)
```

---

### Phase 7.2: Create Test Client

**File:** `test/e2e/workload-aware/workload_aware_test.go`

**Key Features:**
1. **Blocker requests** - Long-running requests to saturate endpoint
2. **Test requests** - Requests with different priorities to test ordering
3. **Timing control** - Precise control over request send times
4. **Result analysis** - Compare dispatch order vs expected priority order

**Pseudo-code:**
```go
func runBasicPriorityTest() {
    // Step 1: Saturate endpoint with blockers
    fmt.Println("Sending blocker requests to saturate endpoint...")
    for i := 0; i < 5; i++ {
        sendBlockerRequest() // Long-running, low priority
    }
    
    // Step 2: Wait for saturation
    time.Sleep(2 * time.Second)
    
    // Step 3: Send test requests (will queue)
    fmt.Println("Sending test requests (will queue)...")
    reqA := sendRequest("low", 1)    // Arrival order: 1st
    reqB := sendRequest("medium", 3) // Arrival order: 2nd
    reqC := sendRequest("high", 5)   // Arrival order: 3rd
    
    // Step 4: Wait for completion
    waitForCompletion([reqA, reqB, reqC])
    
    // Step 5: Analyze dispatch order from logs
    dispatchOrder := getDispatchOrderFromLogs()
    
    // Step 6: Verify
    if dispatchOrder == ["reqC", "reqB", "reqA"] {
        fmt.Println("✓ PASS: Requests dispatched in priority order")
    } else {
        fmt.Println("✗ FAIL: Expected C→B→A, got:", dispatchOrder)
    }
}
```

---

### Phase 7.3: Execution Commands

#### Step 1: Prepare Environment
```bash
# Clean slate
kind delete cluster --name inference-e2e

# Build image with logging
make image-build
```

#### Step 2: Deploy E2E Environment
```bash
# Run e2e tests with extended pause
E2E_PAUSE_ON_EXIT=30m make test-e2e

# Wait for "Pausing for 30m..." message
```

#### Step 3: Run Workload-Aware Tests
```bash
# In another terminal
cd test/e2e/workload-aware

# Run test client
go run workload_aware_test.go \
    --namespace=inf-ext-e2e \
    --scenario=all
```

#### Step 4: Collect and Analyze Logs
```bash
# Get WA-ROUTING logs only
kubectl logs -n inf-ext-e2e deployment/vllm-llama3-8b-instruct-epp \
    | grep "\[WA-ROUTING\]" > wa-routing-logs.txt

# Analyze specific request flow
grep "request_id=req-123" wa-routing-logs.txt

# Check priority scores
grep "Priority score computed" wa-routing-logs.txt

# Check dispatch order
grep "Request dispatched" wa-routing-logs.txt
```

---

## Expected Log Flow for Successful Test

### Scenario: Basic Priority Ordering

**Phase 1: Blockers Saturate Endpoint**
```
[WA-ROUTING] Workload context extracted request_id=blocker-1 workload_id=blocker criticality=1
[WA-ROUTING] Request dispatched request_id=blocker-1 queue_size_before=0
[WA-ROUTING] Workload context extracted request_id=blocker-2 workload_id=blocker criticality=1
[WA-ROUTING] Request dispatched request_id=blocker-2 queue_size_before=0
... (endpoint now saturated)
```

**Phase 2: Test Requests Queue Up**
```
[WA-ROUTING] Workload context extracted request_id=req-A workload_id=low criticality=1
[WA-ROUTING] Request admitted to flow control request_id=req-A workload_id=low criticality=1
[WA-ROUTING] Priority score computed workload_id=low criticality=1 final_score=0.28

[WA-ROUTING] Workload context extracted request_id=req-B workload_id=medium criticality=3
[WA-ROUTING] Request admitted to flow control request_id=req-B workload_id=medium criticality=3
[WA-ROUTING] Priority score computed workload_id=medium criticality=3 final_score=0.52

[WA-ROUTING] Workload context extracted request_id=req-C workload_id=high criticality=5
[WA-ROUTING] Request admitted to flow control request_id=req-C workload_id=high criticality=5
[WA-ROUTING] Priority score computed workload_id=high criticality=5 final_score=0.85
```

**Phase 3: Dispatch in Priority Order (NOT Arrival Order)**
```
[WA-ROUTING] Request dispatched request_id=req-C workload_id=high queue_size_before=3
[WA-ROUTING] Request dispatched request_id=req-B workload_id=medium queue_size_before=2
[WA-ROUTING] Request dispatched request_id=req-A workload_id=low queue_size_before=1
```

**✓ SUCCESS:** Dispatch order (C→B→A) matches priority order, NOT arrival order (A→B→C)

---

## Success Criteria

### Functional Requirements
- [ ] Requests dispatch in priority order when queued (not arrival order)
- [ ] Wait time provides measurable priority boost
- [ ] Request rate penalty observable in priority scores
- [ ] Default workload handles missing headers
- [ ] No errors or crashes

### Observability Requirements
- [ ] All `[WA-ROUTING]` log points present
- [ ] Priority scores match expected formula
- [ ] Dispatch order visible in logs
- [ ] Registry metrics accurate

### Test Scenarios
- [ ] Basic priority ordering (with saturation)
- [ ] Wait time boost (with saturation)
- [ ] Request rate fairness (with saturation)
- [ ] Mixed workload stress test
- [ ] Default workload handling

---

## Key Takeaways

1. **Saturation is Required:** Must saturate endpoint to force queueing
2. **Dispatch ≠ Arrival:** Priority ordering only visible when requests queue
3. **Unique Marker:** `[WA-ROUTING]` makes log analysis efficient
4. **Extended Pause:** 30 minutes provides ample testing time
5. **Single Pod:** Dispatch order = completion order (simplified testing)

---

## Next Steps

1. ✅ **Review and approve this plan**
2. ⏳ **Implement Phase 7.1:** Add logging statements
3. ⏳ **Implement Phase 7.2:** Create test client
4. ⏳ **Execute Phase 7.3:** Run tests
5. ⏳ **Complete Phase 7.4:** Analyze and document results

---

## References

- [Phase 7 E2E Testing Plan](PHASE_7_E2E_TESTING_PLAN.md)
- [Phase 7 Execution Summary](PHASE_7_EXECUTION_SUMMARY.md)
- [Execution Phases](EXECUTION_PHASES.md)
- [Workload-Aware Routing Plan](workload-aware-routing-plan.md)