# Workload-Aware Flow Control - Criticality Test

## Overview

This test verifies that the workload-aware flow control policy correctly prioritizes requests based on **workload criticality** when the system is saturated.

## Scoring Formula

```
Score = (AvgWaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
```

This test focuses on the **Criticality component** (40% weight).

## Test Strategy

1. **Saturate the system** by sending many requests to fill the queue (depth > 5)
2. Send requests from **3 workloads** with different criticality levels:
   - **Background workload**: 50 requests, criticality=1 (LOW), sent at T=0ms
   - **Normal workload**: 30 requests, criticality=3 (MEDIUM), sent at T=200ms
   - **Critical workload**: 20 requests, criticality=5 (HIGH), sent at T=400ms
3. Track completion order and verify high-priority requests complete first

## Expected Behavior

Despite being sent **last**, critical workload requests should complete **first** due to higher criticality score.

**Expected Order:** Critical (5) → Normal (3) → Background (1)

## Prerequisites

1. **Kubernetes cluster** with the inference gateway deployed
2. **vLLM simulator** configured with latency parameters to enable saturation:
   ```yaml
   args:
   - --time-to-first-token
   - "200"
   - --inter-token-latency
   - "50"
   - --max-num-seqs
   - "3"
   ```
3. **Port forwarding** to access the gateway

## How to Run

### Step 1: Set up port forwarding

```bash
kubectl port-forward -n inf-ext-e2e svc/envoy 8081:8081
```

Keep this running in a separate terminal.

### Step 2: Run the test

```bash
cd test/workload-aware
go run workload_aware_client.go
```

## Test Results

### Actual Output

```
=== Workload-Aware Flow Control Test ===

Gateway URL: http://localhost:8081/v1/completions
Workloads:
  1. background-workload (criticality=1, requests=50, delay=0s)
  2. normal-workload (criticality=3, requests=30, delay=200ms)
  3. critical-workload (criticality=5, requests=20, delay=400ms)

[background-workload] Starting workload (criticality=1, requests=50)
[normal-workload] Starting workload (criticality=3, requests=30)
[critical-workload] Starting workload (criticality=5, requests=20)
[critical-workload] Completed all requests
[normal-workload] Completed all requests
[background-workload] Completed all requests

=== Test Results ===

Total Duration: 48.563742417s
Total Requests: 100
  Success: 100
  Failed: 0

=== Completion Order Analysis ===

Average Completion Position by Criticality:
  Criticality 5: Avg position 21.6 (count=20)
  Criticality 3: Avg position 46.5 (count=30)
  Criticality 1: Avg position 64.5 (count=50)

=== Policy Verification ===

Expected Order: Critical (5) < Normal (3) < Background (1)
Actual Avg Positions: Critical=21.6, Normal=46.5, Background=64.5

✅ PASS: Workload-aware policy is working correctly!
   High-priority requests completed before low-priority requests.
```

## Analysis

### Send Order vs Completion Order

**Send Order:**
1. Background (T=0ms) - 50 requests
2. Normal (T=200ms) - 30 requests  
3. Critical (T=400ms) - 20 requests

**Completion Order:**
1. Critical (avg position 21.6) ✅
2. Normal (avg position 46.5) ✅
3. Background (avg position 64.5) ✅

### Key Findings

- **Critical workload** sent last but completed **first** (avg position 21.6)
- **Background workload** sent first but completed **last** (avg position 64.5)
- The policy successfully **reordered the queue** based on criticality
- All 100 requests completed successfully in ~48 seconds

### EPP Logs Evidence

The EPP logs show saturation detection and head-of-line blocking:

```json
{"msg":"Policy's chosen item is saturated; enforcing HoL blocking."}
{"WaitingQueueSize":5}
```

This confirms:
1. System became saturated (queue depth reached threshold of 5)
2. Flow controller stopped dispatching (head-of-line blocking)
3. Workload-aware policy activated to reorder queued requests

## Conclusion

✅ The **criticality-based prioritization** component of the workload-aware policy is working correctly. High-priority workloads are served first when the system is saturated, regardless of arrival order.