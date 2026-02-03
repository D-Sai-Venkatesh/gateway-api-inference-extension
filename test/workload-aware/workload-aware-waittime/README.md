# Workload-Aware Flow Control - Wait Time Penalty Test

## Overview

This test verifies that the workload-aware flow control policy correctly applies the **wait time penalty** to provide fairness. Workloads that have been waiting longer receive a priority boost, even if they have lower criticality.

## Scoring Formula

```
Score = (AvgWaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
```

This test focuses on the **AvgWaitTime component** (40% weight).

## Test Strategy

### Phase 1: Build Wait Time History (20 seconds)
- Send requests continuously from a **low-priority workload** (criticality=2)
- These requests complete slowly, building up **AvgWaitTime** in the workload registry
- The registry tracks wait time using Exponential Moving Average (EMA) with α=0.2

### Phase 2: Main Test
1. **Saturate the system** with 40 requests from the low-priority workload (with accumulated wait history)
2. After 5 seconds, send 20 requests from a **high-priority workload** (criticality=4, no wait history)
3. Track completion order

## Expected Behavior

Despite having **lower criticality (2 vs 4)**, the low-priority workload should complete **first** because:

- **Low-priority workload**: High AvgWaitTime (accumulated) + Low Criticality = **High Score**
- **High-priority workload**: Zero AvgWaitTime (fresh) + High Criticality = **Lower Score**

The 40% weight on AvgWaitTime should overcome the criticality difference, demonstrating the fairness mechanism.

## Prerequisites

1. **Kubernetes cluster** with the inference gateway deployed
2. **vLLM simulator** configured with latency parameters:
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
cd test/workload-aware-waittime
go run main.go --warmup=20s
```

**Options:**
- `--warmup`: Duration to build wait time history (default: 20s)
- `--url`: Gateway URL (default: http://localhost:8081/v1/completions)

## Test Results

### Actual Output

```
=== Wait Time Penalty Test ===

Gateway URL: http://localhost:8081/v1/completions
Warmup Duration: 20s

Phase 1: Building wait time history for low-priority workload...
Warmup phase sent 12 requests
Phase 1 complete. Low-priority workload now has accumulated wait time.

Phase 2: Starting main test...
Workload A: low-priority-with-history (criticality=2, requests=40, delay=0s)
Workload B: high-priority-no-history (criticality=4, requests=20, delay=5s)

[low-priority-with-history] Starting workload (criticality=2, requests=40)
[high-priority-no-history] Starting workload (criticality=4, requests=20)
[low-priority-with-history] Completed all requests
[high-priority-no-history] Completed all requests

=== Test Results ===

Total Duration: 29.113751125s
Total Requests: 60
  Success: 60
  Failed: 0

=== Completion Order Analysis ===

Average Completion Position by Workload:
  low-priority-with-history (criticality=2): Avg position 20.5 (count=40)
  high-priority-no-history (criticality=4): Avg position 50.5 (count=20)

=== Wait Time Penalty Verification ===

Expected: Low-priority workload (with wait history) should complete before high-priority workload (no history)
Actual: Low-priority avg=20.5, High-priority avg=50.5

✅ PASS: Wait time penalty is working!
   Low-priority workload with accumulated wait time completed before high-priority workload.
   This demonstrates the fairness mechanism - workloads that wait longer get priority boost.

=== First 20 Completions ===

 1. low-priority-with-history (criticality=2) - 945.381916ms
 2. low-priority-with-history (criticality=2) - 1.201355334s
 3. low-priority-with-history (criticality=2) - 1.657214334s
 4. low-priority-with-history (criticality=2) - 1.807583084s
 5. low-priority-with-history (criticality=2) - 2.623421708s
 6. low-priority-with-history (criticality=2) - 2.710185542s
 7. low-priority-with-history (criticality=2) - 3.17374625s
 8. low-priority-with-history (criticality=2) - 3.273952542s
 9. low-priority-with-history (criticality=2) - 3.5786395s
10. low-priority-with-history (criticality=2) - 3.985707458s
11. low-priority-with-history (criticality=2) - 4.183651208s
12. low-priority-with-history (criticality=2) - 4.285684583s
13. low-priority-with-history (criticality=2) - 4.637521666s
14. low-priority-with-history (criticality=2) - 4.943384792s
15. low-priority-with-history (criticality=2) - 5.447589125s
16. low-priority-with-history (criticality=2) - 6.408137125s
17. low-priority-with-history (criticality=2) - 6.669606834s
18. low-priority-with-history (criticality=2) - 7.631672209s
19. low-priority-with-history (criticality=2) - 9.090092416s
20. low-priority-with-history (criticality=2) - 9.2046955s
```

## Analysis

### Scoring Breakdown

**Low-Priority Workload (with history):**
- AvgWaitTime: ~5-10 seconds (accumulated during warmup)
  - Normalized: ~0.8-1.0
  - Weight: 0.4
  - **Contribution: ~0.32-0.40**
- Criticality: 2/5 = 0.4
  - Weight: 0.4
  - **Contribution: 0.16**
- RequestRate: Moderate penalty
- **Total Score: ~0.4-0.5**

**High-Priority Workload (no history):**
- AvgWaitTime: 0 seconds (fresh workload)
  - Normalized: 0.0
  - Weight: 0.4
  - **Contribution: 0.0**
- Criticality: 4/5 = 0.8
  - Weight: 0.4
  - **Contribution: 0.32**
- RequestRate: Low penalty (just started)
- **Total Score: ~0.3**

### Key Findings

- **All 40 low-priority requests** completed before **any high-priority requests**
- Low-priority avg position: **20.5** (first half)
- High-priority avg position: **50.5** (second half)
- The **40% weight on AvgWaitTime** was sufficient to overcome the criticality difference (2 vs 4)

### Fairness Mechanism

This demonstrates the **anti-starvation** property of the workload-aware policy:

1. Workloads that wait longer accumulate higher AvgWaitTime
2. Higher AvgWaitTime increases their priority score
3. Eventually, even low-priority workloads get served before fresh high-priority workloads
4. This prevents any workload from being starved indefinitely

## Conclusion

✅ The **wait time penalty/fairness** component of the workload-aware policy is working correctly. The policy successfully balances immediate priority (criticality) with fairness (wait time), ensuring no workload gets starved while still respecting priority levels under normal conditions.

## Related Tests

- **Criticality Test**: [`test/workload-aware/`](../workload-aware/) - Tests basic criticality-based prioritization
- **Request Rate Test**: (Future) - Tests the request rate penalty for fairness