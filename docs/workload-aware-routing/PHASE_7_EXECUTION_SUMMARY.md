# Phase 7: Workload-Aware Routing E2E Testing - Execution Summary

## Overview

This document provides a comprehensive summary of Phase 7 execution plan for end-to-end testing of workload-aware routing functionality.

**Date:** 2026-02-01  
**Status:** Ready for Execution  
**Unique Log Marker:** `[WA-ROUTING]` - Use this to grep logs efficiently

---

## Key Decisions Based on Feedback

### 1. Extended Pause Duration
- Use `E2E_PAUSE_ON_EXIT=30m` (30 minutes instead of 10)
- Provides ample time for manual testing and log analysis

### 2. Unique Log Marker Strategy
- **Marker:** `[WA-ROUTING]`
- **Usage:** Prefix all workload-aware routing logs with this marker
- **Benefit:** Easy to grep and filter logs without overwhelming context window
- **Example:** `grep "[WA-ROUTING]" epp-logs.txt`

### 3. Flow Controller Awareness
- **Critical:** Flow controller only dispatches to **non-saturated** clusters
- **Implication:** Must ensure vLLM sim pod has capacity for all test requests
- **Strategy:** Send requests with delays to avoid saturation, or use small batch sizes

### 4. Simplified Testing
- `make test-e2e` is sufficient - no need for complex setup
- Test client runs during pause window
- Focus on completion order analysis

---

## Execution Phases

### Phase 7.1: Add Comprehensive Logging ✅ READY TO START

**Objective:** Add logging with unique marker `[WA-ROUTING]` throughout the request flow

**Files to Modify:**
1. `pkg/epp/handlers/server.go` - Request ingress
2. `pkg/epp/datastore/workload_registry.go` - Registry operations
3. `pkg/epp/requestcontrol/admission.go` - Flow control admission
4. `pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go` - Priority scoring
5. `pkg/epp/flowcontrol/controller/internal/processor.go` - Dispatch decisions

**Log Marker Format:**
```go
logger.Info("[WA-ROUTING] <message>", "key1", value1, "key2", value2)
```

**Key Log Points:**
- Workload context extraction
- Registry increment/decrement
- Priority score computation
- Dispatch decisions
- Request completion

---

### Phase 7.2: Create Standalone Test Client ✅ READY TO START

**Objective:** Create Go program to send requests and analyze completion order

**File:** `test/e2e/workload-aware/workload_aware_test.go`

**Features:**
- Send HTTP requests with `X-Workload-Context` header
- Track send time and completion time
- Analyze completion order vs expected priority
- Generate detailed report
- Support multiple test scenarios

**Test Scenarios:**
1. Basic priority ordering (high > medium > low)
2. Wait time boost (old request gets priority)
3. Request rate fairness (low-rate workload prioritized)
4. Mixed workload stress test
5. Default workload handling

---

### Phase 7.3: Run E2E Tests ✅ READY TO START

**Objective:** Deploy e2e environment and run workload-aware tests

**Commands:**
```bash
# Step 1: Clean slate
kind delete cluster --name inference-e2e

# Step 2: Build image with logging
make image-build

# Step 3: Run e2e tests with extended pause
E2E_PAUSE_ON_EXIT=30m make test-e2e

# Step 4: In another terminal, run workload-aware tests
cd test/e2e/workload-aware
go run workload_aware_test.go --namespace=inf-ext-e2e

# Step 5: Collect logs (use unique marker)
kubectl logs -n inf-ext-e2e deployment/vllm-llama3-8b-instruct-epp | grep "\[WA-ROUTING\]" > wa-routing-logs.txt
```

---

### Phase 7.4: Analyze Results ✅ READY TO START

**Objective:** Analyze test results and document findings

**Analysis Steps:**
1. Review test client report
2. Grep logs for `[WA-ROUTING]` entries
3. Verify priority scores match expectations
4. Confirm dispatch order matches priority
5. Check for any anomalies or errors

**Success Criteria:**
- High-criticality requests complete first
- Wait time provides priority boost
- Request rate fairness maintained
- No errors or crashes
- All requests complete successfully

---

## Detailed Implementation Plan

### Step 1: Add Logging Statements

#### 1.1 Request Ingress (`pkg/epp/handlers/server.go`)

**Location:** `HandleRequestHeaders()` method, after workload context extraction

```go
if reqCtx.WorkloadContext != nil {
    logger.Info("[WA-ROUTING] Workload context extracted",
        "request_id", reqCtx.RequestID,
        "workload_id", reqCtx.WorkloadContext.WorkloadID,
        "criticality", reqCtx.WorkloadContext.Criticality)
}
```

#### 1.2 Registry Operations (`pkg/epp/datastore/workload_registry.go`)

**Location:** `IncrementActive()` and `DecrementActive()` methods

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

#### 1.3 Flow Control Admission (`pkg/epp/requestcontrol/admission.go`)

**Location:** `Admit()` method, after creating flowControlRequest

```go
if workloadCtx != nil {
    logger.Info("[WA-ROUTING] Request admitted to flow control",
        "request_id", reqCtx.RequestID,
        "workload_id", workloadCtx.workloadID,
        "criticality", workloadCtx.criticality)
}
```

#### 1.4 Priority Scoring (`pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go`)

**Location:** `computeScore()` method, after score calculation

```go
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

#### 1.5 Dispatch Decision (`pkg/epp/flowcontrol/controller/internal/processor.go`)

**Location:** When dispatching request from queue

```go
// Need to find exact location in processor.go where dispatch happens
logger.Info("[WA-ROUTING] Request dispatched",
    "request_id", item.RequestID(),
    "workload_id", workloadID,
    "queue_size", queueSize)
```

---

### Step 2: Create Test Client

#### File Structure
```
test/e2e/workload-aware/
├── workload_aware_test.go    # Main test client
├── scenarios.go               # Test scenario definitions
├── analysis.go                # Result analysis logic
└── README.md                  # Documentation
```

#### Test Client Core Logic

```go
package main

import (
    "encoding/json"
    "fmt"
    "net/http"
    "time"
)

type WorkloadContext struct {
    WorkloadID  string `json:"workload_id"`
    Criticality int    `json:"criticality"`
}

type TestRequest struct {
    ID           string
    WorkloadID   string
    Criticality  int
    SendTime     time.Time
    CompleteTime time.Time
    Duration     time.Duration
    StatusCode   int
}

func sendRequest(envoyURL string, workloadID string, criticality int) (*TestRequest, error) {
    req := &TestRequest{
        ID:          fmt.Sprintf("%s-%d", workloadID, time.Now().UnixNano()),
        WorkloadID:  workloadID,
        Criticality: criticality,
        SendTime:    time.Now(),
    }
    
    // Create workload context header
    ctx := WorkloadContext{
        WorkloadID:  workloadID,
        Criticality: criticality,
    }
    ctxJSON, _ := json.Marshal(ctx)
    
    // Create HTTP request
    httpReq, _ := http.NewRequest("POST", envoyURL+"/v1/completions", 
        strings.NewReader(`{"model":"food-review","prompt":"Test","max_tokens":10}`))
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("X-Workload-Context", string(ctxJSON))
    
    // Send request
    resp, err := http.DefaultClient.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    req.CompleteTime = time.Now()
    req.Duration = req.CompleteTime.Sub(req.SendTime)
    req.StatusCode = resp.StatusCode
    
    return req, nil
}

func main() {
    // Run test scenarios
    fmt.Println("=== Workload-Aware Routing E2E Test ===")
    
    // Scenario 1: Basic Priority
    fmt.Println("\n--- Scenario 1: Basic Priority Ordering ---")
    runBasicPriorityTest()
    
    // Scenario 2: Wait Time Boost
    fmt.Println("\n--- Scenario 2: Wait Time Boost ---")
    runWaitTimeBoostTest()
    
    // Scenario 3: Request Rate Fairness
    fmt.Println("\n--- Scenario 3: Request Rate Fairness ---")
    runRequestRateFairnessTest()
}
```

---

### Step 3: Test Execution Flow

#### 3.1 Pre-Test Setup
```bash
# Ensure clean environment
kind delete cluster --name inference-e2e

# Build image with logging enhancements
make image-build
```

#### 3.2 Deploy E2E Environment
```bash
# Run e2e tests with extended pause
E2E_PAUSE_ON_EXIT=30m make test-e2e

# Wait for tests to complete and cluster to pause
# You'll see: "Pausing for 30m..."
```

#### 3.3 Run Workload-Aware Tests
```bash
# In another terminal
cd test/e2e/workload-aware

# Run test client
go run workload_aware_test.go \
    --envoy-url=http://envoy.inf-ext-e2e.svc:8081 \
    --namespace=inf-ext-e2e \
    --scenario=all \
    --output=results.json
```

#### 3.4 Collect Logs
```bash
# Get EPP logs with WA-ROUTING marker
kubectl logs -n inf-ext-e2e deployment/vllm-llama3-8b-instruct-epp \
    | grep "\[WA-ROUTING\]" > wa-routing-logs.txt

# Count log entries by type
grep "\[WA-ROUTING\] Workload context extracted" wa-routing-logs.txt | wc -l
grep "\[WA-ROUTING\] Priority score computed" wa-routing-logs.txt | wc -l
grep "\[WA-ROUTING\] Request dispatched" wa-routing-logs.txt | wc -l
```

#### 3.5 Analyze Results
```bash
# View test client results
cat results.json | jq .

# Search for specific request in logs
grep "request_id=<ID>" wa-routing-logs.txt

# Analyze priority scores
grep "Priority score computed" wa-routing-logs.txt | grep "workload_id=high"
```

---

## Important Considerations

### Flow Controller Saturation
- **Issue:** Flow controller only dispatches to non-saturated endpoints
- **Solution:** 
  - Send requests with small delays (e.g., 100ms between requests)
  - Use small batch sizes (5-10 requests per scenario)
  - Monitor vLLM sim pod capacity

### Single vLLM Pod
- **Benefit:** Dispatch order = completion order (simplified testing)
- **Limitation:** Can't test multi-pod load balancing
- **Strategy:** Focus on priority ordering within single queue

### Log Volume Management
- **Challenge:** Large log files can overwhelm context window
- **Solution:** Use `[WA-ROUTING]` marker to filter logs
- **Command:** `grep "[WA-ROUTING]" logs.txt` gives only relevant entries

---

## Expected Log Output Examples

### Successful Request Flow
```
[WA-ROUTING] Workload context extracted request_id=req-123 workload_id=high criticality=5
[WA-ROUTING] Workload active incremented workload_id=high active_requests=1 total_requests=1 request_rate=0.0
[WA-ROUTING] Request admitted to flow control request_id=req-123 workload_id=high criticality=5
[WA-ROUTING] Priority score computed workload_id=high criticality=5 avg_wait_time_sec=0.5 request_rate=1.2 final_score=0.76
[WA-ROUTING] Request dispatched request_id=req-123 workload_id=high queue_size=3
[WA-ROUTING] Workload active decremented workload_id=high active_requests=0
```

### Priority Comparison
```
[WA-ROUTING] Priority score computed workload_id=high criticality=5 final_score=0.85
[WA-ROUTING] Priority score computed workload_id=medium criticality=3 final_score=0.52
[WA-ROUTING] Priority score computed workload_id=low criticality=1 final_score=0.28
```

---

## Success Metrics

### Functional Metrics
- [ ] High-criticality requests complete before low-criticality
- [ ] Wait time boost observable in priority scores
- [ ] Request rate penalty applied to high-rate workloads
- [ ] Default workload handles missing headers gracefully
- [ ] No errors or crashes during testing

### Performance Metrics
- [ ] Latency overhead <5ms p99
- [ ] All requests complete successfully
- [ ] No request timeouts

### Observability Metrics
- [ ] All `[WA-ROUTING]` log points present
- [ ] Priority scores match expected formula
- [ ] Dispatch order matches priority order
- [ ] Registry metrics accurate

---

## Next Steps

1. **Implement logging** (Phase 7.1)
2. **Create test client** (Phase 7.2)
3. **Run tests** (Phase 7.3)
4. **Analyze and document** (Phase 7.4)
5. **Create Phase 7 Results document**
6. **Update Execution Phases document**

---

## References

- [Phase 7 E2E Testing Plan](PHASE_7_E2E_TESTING_PLAN.md)
- [Execution Phases](EXECUTION_PHASES.md)
- [Workload-Aware Routing Plan](workload-aware-routing-plan.md)
- [E2E Test Infrastructure](../../test/e2e/epp/README.md)