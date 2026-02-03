# Phase 7: End-to-End Workload-Aware Routing Testing Plan

## Overview

This document outlines the comprehensive end-to-end testing strategy for validating workload-aware routing functionality using the existing e2e infrastructure with a custom test client.

**Date:** 2026-02-01  
**Status:** Ready for Implementation

---

## Testing Strategy

### Key Insight
Since the e2e environment has **only 1 vLLM sim pod**, the **dispatch order equals the completion order**. This makes it straightforward to verify that workload-aware prioritization is working correctly by observing the order in which requests complete.

### Approach
1. **Use existing e2e infrastructure** (`make test-e2e`)
2. **Pause cleanup** using `E2E_PAUSE_ON_EXIT` environment variable
3. **Add comprehensive logging** throughout the request flow
4. **Create standalone test client** that sends requests with different workload contexts
5. **Verify completion order** matches expected priority order

---

## Test Environment Setup

### Prerequisites
```bash
# Delete existing kind cluster (if any)
kind delete cluster --name inference-e2e

# Set environment variables
export E2E_PAUSE_ON_EXIT=10m  # Keep cluster alive for 10 minutes
export E2E_NS=inf-ext-e2e
export E2E_IMAGE=controller:latest
```

### Deployment Steps
```bash
# 1. Build the image with logging enhancements
make image-build

# 2. Run e2e tests (will create cluster and deploy everything)
make test-e2e

# 3. Cluster will pause for 10 minutes after tests complete
# During this time, run the workload-aware test client
```

---

## Logging Enhancements

### Locations to Add Logging

#### 1. Request Ingress (`pkg/epp/handlers/server.go`)
```go
// In HandleRequestHeaders()
logger.Info("Workload context extracted",
    "workload_id", reqCtx.WorkloadContext.WorkloadID,
    "criticality", reqCtx.WorkloadContext.Criticality,
    "request_id", reqCtx.RequestID)
```

#### 2. Registry Updates (`pkg/epp/datastore/workload_registry.go`)
```go
// In IncrementActive()
logger.V(2).Info("Workload active requests incremented",
    "workload_id", workloadID,
    "active_requests", metrics.ActiveRequests,
    "total_requests", metrics.TotalRequests)

// In DecrementActive()
logger.V(2).Info("Workload active requests decremented",
    "workload_id", workloadID,
    "active_requests", metrics.ActiveRequests)
```

#### 3. Flow Control Admission (`pkg/epp/requestcontrol/admission.go`)
```go
// In Admit()
logger.Info("Request admitted to flow control",
    "request_id", reqCtx.RequestID,
    "workload_id", workloadCtx.WorkloadID,
    "criticality", workloadCtx.Criticality)
```

#### 4. Priority Scoring (`pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go`)
```go
// In computeScore()
logger.V(2).Info("Priority score computed",
    "workload_id", workloadID,
    "criticality", criticality,
    "avg_wait_time", avgWaitTime,
    "request_rate", requestRate,
    "score", score)
```

#### 5. Dispatch Decision (`pkg/epp/flowcontrol/controller/internal/processor.go`)
```go
// When dispatching request
logger.Info("Request dispatched",
    "request_id", item.RequestID(),
    "workload_id", workloadID,
    "priority_score", score,
    "queue_position", position)
```

---

## Test Scenarios

### Scenario 1: Basic Priority Ordering
**Objective:** Verify high-criticality requests are dispatched before low-criticality requests

**Test Steps:**
1. Send 3 requests simultaneously:
   - Request A: `workload_id=low`, `criticality=1`
   - Request B: `workload_id=medium`, `criticality=3`
   - Request C: `workload_id=high`, `criticality=5`

**Expected Order:** C → B → A

**Verification:**
- Check completion timestamps
- Verify C completes first, then B, then A
- Tolerance: ±2 seconds (for network/processing variance)

---

### Scenario 2: Wait Time Boost
**Objective:** Verify older requests get priority boost over time

**Test Steps:**
1. Send Request A: `workload_id=waiting`, `criticality=2`
2. Wait 30 seconds
3. Send Request B: `workload_id=new`, `criticality=4`

**Expected Behavior:**
- Request A should get significant wait time boost
- Depending on weights, A might dispatch before B despite lower criticality

**Verification:**
- Check priority scores in logs
- Verify wait time component increases for Request A
- Compare final dispatch order

---

### Scenario 3: Request Rate Fairness
**Objective:** Verify high-rate workloads are penalized

**Test Steps:**
1. Send 10 requests rapidly: `workload_id=flood`, `criticality=4`
2. Send 1 request: `workload_id=fair`, `criticality=4`

**Expected Behavior:**
- `flood` workload should have high request rate
- `fair` workload should get priority despite same criticality

**Verification:**
- Check request rate metrics in logs
- Verify `fair` request dispatches before some `flood` requests
- Confirm rate penalty is applied

---

### Scenario 4: Mixed Workload Stress Test
**Objective:** Verify system handles complex mixed workload scenarios

**Test Steps:**
1. Send 20 requests with varying:
   - 3 workload IDs (low, medium, high)
   - Criticality levels (1-5)
   - Arrival times (staggered)

**Expected Behavior:**
- Requests dispatched according to priority formula
- No starvation of low-priority requests
- Fair distribution across workloads

**Verification:**
- Analyze completion order vs expected priority
- Check for any anomalies or unexpected behavior
- Verify all requests complete successfully

---

### Scenario 5: Default Workload Handling
**Objective:** Verify requests without workload context are handled correctly

**Test Steps:**
1. Send requests without `X-Workload-Context` header
2. Send requests with invalid JSON in header
3. Send requests with out-of-range criticality

**Expected Behavior:**
- All treated as default workload with criticality=3
- No errors or crashes
- Graceful degradation

**Verification:**
- Check logs for default workload assignment
- Verify requests complete successfully
- Confirm no error messages

---

## Test Client Implementation

### File: `test/e2e/workload-aware/workload_aware_test.go`

**Key Features:**
- Standalone Go program (not Ginkgo test)
- Sends HTTP requests with `X-Workload-Context` header
- Tracks request send time and completion time
- Analyzes completion order
- Generates detailed report

**Request Structure:**
```go
type TestRequest struct {
    ID           string
    WorkloadID   string
    Criticality  int
    SendTime     time.Time
    CompleteTime time.Time
    Duration     time.Duration
    Response     string
}
```

**Test Flow:**
1. Connect to Envoy service in cluster
2. Send requests according to test scenario
3. Track completion order
4. Analyze results
5. Generate pass/fail report

---

## Execution Commands

### Step 1: Prepare Environment
```bash
# Clean slate
kind delete cluster --name inference-e2e

# Build image with logging
make image-build
```

### Step 2: Deploy E2E Environment
```bash
# Run e2e tests with pause
E2E_PAUSE_ON_EXIT=10m make test-e2e
```

### Step 3: Run Workload-Aware Tests
```bash
# In another terminal (while cluster is paused)
cd test/e2e/workload-aware
go run workload_aware_test.go \
    --namespace=inf-ext-e2e \
    --envoy-service=envoy \
    --envoy-port=8081 \
    --scenario=all
```

### Step 4: Collect Logs
```bash
# EPP logs
kubectl logs -n inf-ext-e2e deployment/vllm-llama3-8b-instruct-epp > epp-logs.txt

# vLLM sim logs
kubectl logs -n inf-ext-e2e deployment/vllm-llama3-8b-instruct > vllm-logs.txt

# Test client output
# Already captured in terminal
```

### Step 5: Analyze Results
```bash
# Review test client report
cat workload-aware-test-report.txt

# Search for priority scores in EPP logs
grep "Priority score computed" epp-logs.txt

# Search for dispatch decisions
grep "Request dispatched" epp-logs.txt
```

---

## Success Criteria

### Functional Requirements
- [ ] High-criticality requests dispatch before low-criticality
- [ ] Wait time provides priority boost over time
- [ ] High request-rate workloads are fairly throttled
- [ ] Default workload handles missing/invalid headers
- [ ] No crashes or errors during testing

### Performance Requirements
- [ ] Latency overhead <5ms p99 (compared to baseline)
- [ ] All requests complete successfully
- [ ] No request timeouts or failures

### Observability Requirements
- [ ] Logs show workload context extraction
- [ ] Logs show priority score computation
- [ ] Logs show dispatch decisions
- [ ] Metrics reflect workload state accurately

---

## Troubleshooting

### Issue: Requests not prioritized correctly
**Debug Steps:**
1. Check EPP logs for workload context extraction
2. Verify priority scores are being computed
3. Check if comparator is being called
4. Verify WorkloadRegistry has correct metrics

### Issue: Test client can't connect to Envoy
**Debug Steps:**
1. Verify Envoy service is running: `kubectl get svc -n inf-ext-e2e`
2. Check Envoy pod status: `kubectl get pods -n inf-ext-e2e`
3. Port-forward if needed: `kubectl port-forward -n inf-ext-e2e svc/envoy 8081:8081`

### Issue: Logs not showing expected information
**Debug Steps:**
1. Verify log level is set to at least INFO (v=4)
2. Check if logging statements were added correctly
3. Rebuild and redeploy image
4. Check for any log filtering in deployment

---

## Next Steps After Testing

### If Tests Pass
1. Document results in `PHASE_7_RESULTS.md`
2. Create performance baseline metrics
3. Proceed to Phase 8: Production Rollout Planning
4. Update execution phases document

### If Tests Fail
1. Analyze failure patterns
2. Review logs for root cause
3. Fix identified issues
4. Re-run tests
5. Document lessons learned

---

## Files to Create/Modify

### New Files
- `test/e2e/workload-aware/workload_aware_test.go` - Standalone test client
- `test/e2e/workload-aware/README.md` - Test client documentation
- `test/e2e/workload-aware/scenarios.go` - Test scenario definitions
- `test/e2e/workload-aware/analysis.go` - Result analysis logic

### Files to Modify (Add Logging)
- `pkg/epp/handlers/server.go` - Request ingress logging
- `pkg/epp/datastore/workload_registry.go` - Registry operation logging
- `pkg/epp/requestcontrol/admission.go` - Admission logging
- `pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go` - Score computation logging
- `pkg/epp/flowcontrol/controller/internal/processor.go` - Dispatch logging

---

## Timeline

- **Day 1:** Add logging statements, build and test locally
- **Day 2:** Implement test client and scenarios
- **Day 3:** Run full test suite, analyze results
- **Day 4:** Fix any issues, re-test, document results

---

## References

- [Original Implementation Plan](workload-aware-routing-plan.md)
- [Execution Phases](EXECUTION_PHASES.md)
- [E2E Test Infrastructure](../../test/e2e/epp/README.md)
- [Phase 5 Testing Plan](PHASE_5_E2E_TESTING_PLAN.md)