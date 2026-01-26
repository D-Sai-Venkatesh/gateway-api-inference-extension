# Phase 5: End-to-End Testing Plan (Enhanced with Edge Cases)

## Overview
This phase validates the complete workload-aware routing implementation through comprehensive manual testing with real requests, including extensive edge case coverage.

## Prerequisites

### 1. Build the EPP Binary
```bash
# From project root
make build
# Or build directly
go build -o bin/epp ./cmd/epp
```

### 2. Create Test Configuration

Create `test-config/workload-aware-config.yaml`:
```yaml
apiVersion: inference.networking.x-k8s.io/v1alpha1
kind: EndpointPickerConfig
plugins:
  - type: workload-aware-ordering-policy
    name: workload-aware-ordering-policy
  - type: queue-scorer
  - type: kv-cache-utilization-scorer
  - type: prefix-cache-scorer
schedulingProfiles:
  - name: default
    plugins:
      - pluginRef: queue-scorer
      - pluginRef: kv-cache-utilization-scorer
      - pluginRef: prefix-cache-scorer
featureGates:
  - flowControl
flowControl:
  registry:
    initialShardCount: 1
    flowGCTimeout: 5m
    priorityBandGCTimeout: 10m
    defaultPriorityBand:
      priority: 0
      priorityName: "Default"
      orderingPolicy: workload-aware-ordering-policy
      queue: ListQueue
      maxBytes: 1000000000
    priorityBands:
      - priority: 5
        priorityName: "Critical"
        orderingPolicy: workload-aware-ordering-policy
        queue: ListQueue
        maxBytes: 2000000000
      - priority: 3
        priorityName: "High"
        orderingPolicy: workload-aware-ordering-policy
        queue: ListQueue
        maxBytes: 1500000000
      - priority: 1
        priorityName: "Low"
        orderingPolicy: workload-aware-ordering-policy
        queue: ListQueue
        maxBytes: 500000000
```

### 3. Setup Mock Backend (Optional)
For testing without a real model server, create a simple mock:

```bash
# Create mock-backend.go
cat > test-config/mock-backend.go << 'EOF'
package main

import (
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"
)

func main() {
    http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
        // Simulate processing time
        time.Sleep(100 * time.Millisecond)
        
        response := map[string]interface{}{
            "id": "mock-response",
            "object": "chat.completion",
            "created": time.Now().Unix(),
            "model": "mock-model",
            "choices": []map[string]interface{}{
                {
                    "index": 0,
                    "message": map[string]string{
                        "role": "assistant",
                        "content": "Mock response",
                    },
                    "finish_reason": "stop",
                },
            },
        }
        
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(response)
    })
    
    log.Println("Mock backend listening on :8000")
    log.Fatal(http.ListenAndServe(":8000", nil))
}
EOF

# Run mock backend
go run test-config/mock-backend.go &
```

---

## Core Test Scenarios

### Scenario 1: Basic Priority Ordering

**Objective:** Verify that high-criticality requests are prioritized over low-criticality requests.

**Setup:**
```bash
# Terminal 1: Start EPP with debug logging
./bin/epp \
  --pool-name test-pool \
  --pool-namespace default \
  --config-file test-config/workload-aware-config.yaml \
  --v 4 \
  --zap-encoder json
```

**Test Steps:**
```bash
# Terminal 2: Send low-priority request (should queue)
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"low-priority-workload\",\"criticality\":1}" \
  -d '{
    "model": "test-model",
    "messages": [{"role": "user", "content": "Low priority task"}]
  }' &

# Wait a moment, then send high-priority request
sleep 1

# Terminal 3: Send high-priority request (should jump queue)
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"high-priority-workload\",\"criticality\":5}" \
  -d '{
    "model": "test-model",
    "messages": [{"role": "user", "content": "High priority task"}]
  }'
```

**Expected Results:**
- ✅ High-priority request completes first despite arriving later
- ✅ Logs show workload context extraction: `"workload_id":"high-priority-workload","criticality":5`
- ✅ Logs show priority score computation with higher score for high-criticality request
- ✅ No errors or panics

---

### Scenario 2: Wait Time Boost

**Objective:** Verify that requests waiting longer receive priority boost to prevent starvation.

**Test Steps:**
```bash
# Send medium-priority request and let it wait
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"waiting-workload\",\"criticality\":2}" \
  -d '{
    "model": "test-model",
    "messages": [{"role": "user", "content": "Waiting request"}]
  }' &

# Wait 30 seconds to accumulate wait time
sleep 30

# Send newer high-priority request
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"new-workload\",\"criticality\":3}" \
  -d '{
    "model": "test-model",
    "messages": [{"role": "user", "content": "New request"}]
  }'
```

**Expected Results:**
- ✅ Waiting request may dispatch first due to wait time boost (depending on exact timing)
- ✅ Logs show wait time factor in score computation
- ✅ Score increases over time for waiting request

---

### Scenario 3: Request Rate Fairness

**Objective:** Verify that high request-rate workloads don't starve low request-rate workloads.

**Test Steps:**
```bash
# Flood with workload A (50 requests)
for i in {1..50}; do
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "Content-Type: application/json" \
    -H "X-Workload-Context: {\"workload_id\":\"flood-workload\",\"criticality\":4}" \
    -d "{
      \"model\": \"test-model\",
      \"messages\": [{\"role\": \"user\", \"content\": \"Flood request $i\"}]
    }" &
done

# Wait a moment, then send single request from workload B
sleep 2

curl -X POST http://localhost:9002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"fair-workload\",\"criticality\":4}" \
  -d '{
    "model": "test-model",
    "messages": [{"role": "user", "content": "Fair request"}]
  }'
```

**Expected Results:**
- ✅ Workload B gets fair treatment despite lower request count
- ✅ Request rate penalty applied to flood-workload
- ✅ Logs show request rate tracking and penalty calculation
- ✅ fair-workload request doesn't wait excessively long

---

### Scenario 4: Default Workload (Missing Headers)

**Objective:** Verify graceful handling of requests without workload context headers.

**Test Steps:**
```bash
# Send request without X-Workload-Context header
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "test-model",
    "messages": [{"role": "user", "content": "No workload context"}]
  }'
```

**Expected Results:**
- ✅ Request succeeds without errors
- ✅ Logs show default workload assignment: `"workload_id":"default"`
- ✅ Default criticality (3) applied
- ✅ No crashes or panics

---

### Scenario 5: Invalid Workload Context

**Objective:** Verify handling of malformed workload context headers.

**Test Steps:**
```bash
# Test 1: Invalid JSON
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {invalid-json}" \
  -d '{
    "model": "test-model",
    "messages": [{"role": "user", "content": "Invalid JSON"}]
  }'

# Test 2: Missing criticality
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"test\"}" \
  -d '{
    "model": "test-model",
    "messages": [{"role": "user", "content": "Missing criticality"}]
  }'

# Test 3: Out of range criticality
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"test\",\"criticality\":10}" \
  -d '{
    "model": "test-model",
    "messages": [{"role": "user", "content": "Out of range"}]
  }'
```

**Expected Results:**
- ✅ All requests succeed with default values
- ✅ Logs show validation warnings
- ✅ Invalid values replaced with defaults
- ✅ No crashes or panics

---

### Scenario 6: Mixed Priority Workloads

**Objective:** Verify correct ordering with multiple workloads at different priorities.

**Test Steps:**
```bash
# Send requests in random order with different priorities
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"w1\",\"criticality\":2}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Request 1"}]}' &

curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"w2\",\"criticality\":5}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Request 2"}]}' &

curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"w3\",\"criticality\":1}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Request 3"}]}' &

curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"w4\",\"criticality\":4}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Request 4"}]}' &

curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"w5\",\"criticality\":3}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Request 5"}]}' &
```

**Expected Results:**
- ✅ Requests dispatched in priority order: w2(5) → w4(4) → w5(3) → w1(2) → w3(1)
- ✅ Logs show correct score computation for each request
- ✅ All requests complete successfully

---

## Edge Case Test Scenarios

### Scenario 7: Concurrent Same-Priority Requests

**Objective:** Verify fair handling when multiple requests have identical priority and criticality.

**Test Steps:**
```bash
# Send 10 requests with identical priority simultaneously
for i in {1..10}; do
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "X-Workload-Context: {\"workload_id\":\"same-priority-$i\",\"criticality\":3}" \
    -d "{\"model\":\"test\",\"messages\":[{\"role\":\"user\",\"content\":\"Request $i\"}]}" &
done
```

**Expected Results:**
- ✅ All requests complete successfully
- ✅ Fair distribution among workloads (no single workload monopolizes)
- ✅ Request rate tracking works correctly for multiple workloads
- ✅ No deadlocks or race conditions

---

### Scenario 8: Workload Registry Cleanup (GC)

**Objective:** Verify that idle workloads are garbage collected after timeout.

**Test Steps:**
```bash
# Send request from workload A
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"gc-test-workload\",\"criticality\":3}" \
  -d '{"model":"test","messages":[{"role":"user","content":"GC test"}]}'

# Check metrics - workload should exist
curl http://localhost:9090/metrics | grep "gc-test-workload"

# Wait for GC timeout (default: 5 minutes) + buffer
sleep 330

# Check metrics again - workload should be cleaned up
curl http://localhost:9090/metrics | grep "gc-test-workload"
```

**Expected Results:**
- ✅ Workload appears in metrics initially
- ✅ Workload removed from metrics after GC timeout
- ✅ Logs show GC activity
- ✅ No memory leaks

---

### Scenario 9: Dynamic Priority Band Creation

**Objective:** Verify that priority bands are created dynamically for unconfigured priorities.

**Test Steps:**
```bash
# Send request with priority not in config (e.g., priority 2)
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"dynamic-band\",\"criticality\":2}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Dynamic band test"}]}'

# Check logs for dynamic band creation
grep "dynamic.*priority.*band" epp.log
```

**Expected Results:**
- ✅ Request succeeds
- ✅ Dynamic priority band created with priority=2
- ✅ Band uses DefaultPriorityBand template configuration
- ✅ WorkloadRegistry properly injected into dynamic band's OrderingPolicy
- ✅ Logs show band creation

---

### Scenario 10: Zero Criticality Edge Case

**Objective:** Verify handling of criticality=0 (edge of valid range).

**Test Steps:**
```bash
# Send request with criticality=0
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"zero-criticality\",\"criticality\":0}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Zero criticality"}]}'
```

**Expected Results:**
- ✅ Request succeeds
- ✅ Criticality clamped to minimum valid value (1)
- ✅ Logs show validation and clamping
- ✅ No crashes

---

### Scenario 11: Negative Criticality Edge Case

**Objective:** Verify handling of negative criticality values.

**Test Steps:**
```bash
# Send request with negative criticality
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"negative-criticality\",\"criticality\":-5}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Negative criticality"}]}'
```

**Expected Results:**
- ✅ Request succeeds
- ✅ Criticality clamped to minimum valid value (1)
- ✅ Logs show validation warning
- ✅ No crashes

---

### Scenario 12: Very Long Workload ID

**Objective:** Verify handling of extremely long workload IDs.

**Test Steps:**
```bash
# Generate 1000-character workload ID
LONG_ID=$(python3 -c "print('a' * 1000)")

curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"$LONG_ID\",\"criticality\":3}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Long ID test"}]}'
```

**Expected Results:**
- ✅ Request succeeds or fails gracefully
- ✅ No buffer overflows
- ✅ Appropriate error message if ID too long
- ✅ No crashes

---

### Scenario 13: Special Characters in Workload ID

**Objective:** Verify handling of special characters and Unicode in workload IDs.

**Test Steps:**
```bash
# Test various special characters
curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":"test-with-dashes","criticality":3}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Dashes"}]}'

curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":"test_with_underscores","criticality":3}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Underscores"}]}'

curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":"test.with.dots","criticality":3}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Dots"}]}'

curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":"test/with/slashes","criticality":3}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Slashes"}]}'

curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":"test with spaces","criticality":3}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Spaces"}]}'

# Unicode test
curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":"test-日本語-emoji-🚀","criticality":3}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Unicode"}]}'
```

**Expected Results:**
- ✅ All valid characters handled correctly
- ✅ Invalid characters either accepted or rejected gracefully
- ✅ Unicode handled properly
- ✅ No crashes or encoding issues

---

### Scenario 14: Empty Workload ID

**Objective:** Verify handling of empty workload ID.

**Test Steps:**
```bash
# Send request with empty workload_id
curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":"","criticality":3}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Empty ID"}]}'
```

**Expected Results:**
- ✅ Request succeeds with default workload ID
- ✅ Logs show validation and default assignment
- ✅ No crashes

---

### Scenario 15: Rapid Priority Changes

**Objective:** Verify system stability when same workload rapidly changes priority.

**Test Steps:**
```bash
# Send 20 requests from same workload with alternating priorities
for i in {1..20}; do
  CRIT=$((i % 5 + 1))
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "X-Workload-Context: {\"workload_id\":\"changing-priority\",\"criticality\":$CRIT}" \
    -d "{\"model\":\"test\",\"messages\":[{\"role\":\"user\",\"content\":\"Request $i\"}]}" &
done
```

**Expected Results:**
- ✅ All requests complete successfully
- ✅ Registry correctly tracks workload across priority changes
- ✅ No race conditions or data corruption
- ✅ Metrics remain consistent

---

### Scenario 16: Request Rate Spike and Recovery

**Objective:** Verify system handles sudden request rate spikes and recovers gracefully.

**Test Steps:**
```bash
# Phase 1: Normal load
for i in {1..5}; do
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "X-Workload-Context: {\"workload_id\":\"spike-test\",\"criticality\":3}" \
    -d '{"model":"test","messages":[{"role":"user","content":"Normal"}]}' &
  sleep 1
done

# Phase 2: Sudden spike (100 requests)
for i in {1..100}; do
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "X-Workload-Context: {\"workload_id\":\"spike-test\",\"criticality\":3}" \
    -d "{\"model\":\"test\",\"messages\":[{\"role\":\"user\",\"content\":\"Spike $i\"}]}" &
done

# Phase 3: Wait for recovery
sleep 60

# Phase 4: Normal load again
for i in {1..5}; do
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "X-Workload-Context: {\"workload_id\":\"spike-test\",\"criticality\":3}" \
    -d '{"model":"test","messages":[{"role":"user","content":"Recovery"}]}' &
  sleep 1
done
```

**Expected Results:**
- ✅ System handles spike without crashes
- ✅ Request rate penalty applied during spike
- ✅ Penalty decays after spike ends
- ✅ Normal behavior resumes after recovery
- ✅ Metrics show rate spike and recovery

---

### Scenario 17: Multiple Workloads with Same Criticality but Different Rates

**Objective:** Verify fairness when workloads have same priority but different request rates.

**Test Steps:**
```bash
# Workload A: High rate (30 requests)
for i in {1..30}; do
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "X-Workload-Context: {\"workload_id\":\"high-rate\",\"criticality\":3}" \
    -d "{\"model\":\"test\",\"messages\":[{\"role\":\"user\",\"content\":\"High rate $i\"}]}" &
done

# Workload B: Medium rate (10 requests)
for i in {1..10}; do
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "X-Workload-Context: {\"workload_id\":\"medium-rate\",\"criticality\":3}" \
    -d "{\"model\":\"test\",\"messages\":[{\"role\":\"user\",\"content\":\"Medium rate $i\"}]}" &
done

# Workload C: Low rate (3 requests)
for i in {1..3}; do
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "X-Workload-Context: {\"workload_id\":\"low-rate\",\"criticality\":3}" \
    -d "{\"model\":\"test\",\"messages\":[{\"role\":\"user\",\"content\":\"Low rate $i\"}]}" &
done
```

**Expected Results:**
- ✅ Low-rate workload gets proportionally better treatment
- ✅ High-rate workload receives penalty
- ✅ All workloads make progress (no starvation)
- ✅ Fairness metrics show balanced distribution

---

### Scenario 18: Workload Registry State Consistency

**Objective:** Verify registry maintains consistent state across request lifecycle.

**Test Steps:**
```bash
# Send request and monitor metrics at each stage
curl -X POST http://localhost:9002/v1/chat/completions \
  -H "X-Workload-Context: {\"workload_id\":\"consistency-test\",\"criticality\":3}" \
  -d '{"model":"test","messages":[{"role":"user","content":"Consistency test"}]}' &

# Check metrics during request
curl http://localhost:9090/metrics | grep "consistency-test"

# Wait for completion
sleep 5

# Check metrics after completion
curl http://localhost:9090/metrics | grep "consistency-test"
```

**Expected Results:**
- ✅ Active requests count increases when request arrives
- ✅ Active requests count decreases when request completes
- ✅ Total requests count increments correctly
- ✅ No orphaned entries in registry

---

### Scenario 19: Null/Undefined Fields in Workload Context

**Objective:** Verify handling of null or undefined fields in JSON.

**Test Steps:**
```bash
# Test null workload_id
curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":null,"criticality":3}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Null ID"}]}'

# Test null criticality
curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":"test","criticality":null}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Null criticality"}]}'

# Test both null
curl -X POST http://localhost:9002/v1/chat/completions \
  -H 'X-Workload-Context: {"workload_id":null,"criticality":null}' \
  -d '{"model":"test","messages":[{"role":"user","content":"Both null"}]}'
```

**Expected Results:**
- ✅ All requests succeed with defaults
- ✅ Null values replaced with defaults
- ✅ Logs show validation
- ✅ No crashes

---

### Scenario 20: Stress Test - Sustained High Load

**Objective:** Verify system stability under sustained high load.

**Test Steps:**
```bash
# Run sustained load for 5 minutes (300 requests at 1 req/sec)
for i in {1..300}; do
  WORKLOAD=$((i % 10))
  CRIT=$((i % 5 + 1))
  curl -X POST http://localhost:9002/v1/chat/completions \
    -H "X-Workload-Context: {\"workload_id\":\"stress-workload-$WORKLOAD\",\"criticality\":$CRIT}" \
    -d "{\"model\":\"test\",\"messages\":[{\"role\":\"user\",\"content\":\"Stress $i\"}]}" &
  sleep 1
done
```

**Expected Results:**
- ✅ System remains stable throughout test
- ✅ No memory leaks (monitor with `top` or `htop`)
- ✅ No goroutine leaks
- ✅ Response times remain reasonable
- ✅ All requests complete successfully
- ✅ Metrics remain accurate

---

## Observability Testing

### Check Metrics

```bash
# Query Prometheus metrics endpoint
curl http://localhost:9090/metrics | grep -E "workload|flow_control"

# Expected metrics:
# - workload_active_requests{workload_id="..."}
# - workload_total_requests{workload_id="..."}
# - workload_request_rate{workload_id="..."}
# - flow_control_queue_depth{priority="..."}
# - flow_control_dispatch_latency_seconds{...}
```

### Check Debug Logs

Enable debug logging and verify log output:

```bash
# Look for key log entries:
grep "workload context extracted" epp.log
grep "priority score computed" epp.log
grep "dispatch decision" epp.log
grep "registry updated" epp.log
grep "request rate" epp.log
```

**Expected Log Patterns:**
```json
{"level":"debug","msg":"workload context extracted","workload_id":"test","criticality":4}
{"level":"debug","msg":"priority score computed","workload_id":"test","score":4.5,"wait_time_ms":1500}
{"level":"debug","msg":"dispatch decision","workload_id":"test","selected":true}
{"level":"debug","msg":"registry updated","workload_id":"test","active_requests":5,"total_requests":42}
```

---

## Validation Checklist

After completing all test scenarios, verify:

### Core Functionality
- [ ] **Priority Ordering**: High-criticality requests prioritized correctly
- [ ] **Wait Time Boost**: Long-waiting requests receive priority boost
- [ ] **Request Rate Fairness**: High request-rate workloads fairly throttled
- [ ] **Default Workload**: Missing headers handled gracefully with defaults
- [ ] **Invalid Input**: Malformed headers don't cause crashes
- [ ] **Mixed Workloads**: Multiple workloads ordered correctly

### Edge Cases
- [ ] **Concurrent Same-Priority**: Fair handling of identical priorities
- [ ] **GC Cleanup**: Idle workloads garbage collected correctly
- [ ] **Dynamic Bands**: Priority bands created dynamically as needed
- [ ] **Zero/Negative Criticality**: Edge values handled correctly
- [ ] **Long Workload IDs**: Very long IDs handled or rejected gracefully
- [ ] **Special Characters**: Special chars and Unicode handled correctly
- [ ] **Empty Workload ID**: Empty IDs default correctly
- [ ] **Rapid Priority Changes**: System stable with frequent changes
- [ ] **Request Rate Spikes**: System handles and recovers from spikes
- [ ] **Multiple Rate Workloads**: Fairness across different rates
- [ ] **State Consistency**: Registry maintains consistent state
- [ ] **Null Fields**: Null/undefined fields handled correctly
- [ ] **Sustained Load**: System stable under sustained high load

### Observability
- [ ] **Metrics Exposed**: All expected metrics available
- [ ] **Metrics Accurate**: Metrics reflect actual system state
- [ ] **Logs Clear**: Debug logs provide clear visibility
- [ ] **No Memory Leaks**: Memory usage stable over time
- [ ] **No Goroutine Leaks**: Goroutine count stable

### Performance
- [ ] **No Crashes**: System remains stable under all scenarios
- [ ] **No Panics**: No unhandled panics occur
- [ ] **Acceptable Latency**: Response times remain reasonable
- [ ] **Resource Usage**: CPU and memory usage acceptable

---

## Troubleshooting

### Issue: EPP fails to start
**Check:**
- Config file syntax is valid YAML
- Plugin name matches registered plugin: `workload-aware-ordering-policy`
- All required fields present in config
- FlowControl feature gate enabled

### Issue: Requests not prioritized correctly
**Check:**
- X-Workload-Context header is properly formatted JSON
- Criticality values are in valid range (1-5)
- FlowControl feature gate is enabled
- Debug logs show workload context extraction
- WorkloadRegistry properly injected into policies

### Issue: Metrics not appearing
**Check:**
- Metrics endpoint is accessible: `curl http://localhost:9090/metrics`
- Prometheus scraping is configured correctly
- Metrics registration succeeded (check startup logs)

### Issue: High memory usage
**Check:**
- Flow GC timeout is reasonable (default: 5 minutes)
- Priority band GC timeout is set (default: 10 minutes)
- No memory leaks in registry (monitor over time)
- Check for goroutine leaks: `curl http://localhost:6060/debug/pprof/goroutine`

### Issue: Inconsistent behavior
**Check:**
- Registry state consistency (check metrics)
- No race conditions (run with `-race` flag during development)
- Proper synchronization in concurrent code
- Request lifecycle tracking working correctly

---

## Success Criteria

Phase 5 is complete when:

1. ✅ All 20 test scenarios pass successfully (6 core + 14 edge cases)
2. ✅ All items in validation checklist are verified
3. ✅ Metrics are exposed and accurate
4. ✅ Debug logs provide clear visibility
5. ✅ No crashes, panics, or errors under normal operation
6. ✅ System handles all edge cases gracefully
7. ✅ Performance is acceptable (no significant latency increase)
8. ✅ No memory or goroutine leaks detected
9. ✅ State consistency maintained across all scenarios
10. ✅ System recovers gracefully from stress conditions

---

## Next Steps

After Phase 5 completion:
- Document any issues found and fixes applied
- Create summary report of test results
- Proceed to **Phase 6: Performance & Load Testing**
- Consider adding automated integration tests based on manual test scenarios
- Update documentation with any discovered limitations or caveats