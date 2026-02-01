# Workload Registry - Simple Interaction Diagram

## Request Lifecycle with Workload Tracking

```
┌─────────────────────────────────────────────────────────────────────┐
│  CLIENT REQUEST                                                     │
│  Header: X-Workload-Context: {"workload_id":"job-123","criticality":4}│
└─────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
                    ┌──────────────────┐
                    │ Extract Context  │
                    │ from Headers     │
                    └──────────────────┘
                              │
                              ▼
        ┌─────────────────────────────────────────────────┐
        │  ⚡ TRACK: WorkloadHandleNewRequest()          │
        │  • TotalRequests++                             │
        │  • ActiveRequests++                            │
        │  • Update RequestRate                          │
        └─────────────────────────────────────────────────┘
                              │
                              ▼
                    ┌──────────────────┐
                    │  Enter Queue     │
                    │  (Wait for turn) │
                    └──────────────────┘
                              │
                              │  ┌────────────────────────────┐
                              │  │ CONCURRENT:                │
                              │  │ Priority Evaluation        │
                              │  │                            │
                              │  │ Read Metrics:              │
                              │  │ • AverageWaitTime (EMA)    │
                              │  │ • RequestRate              │
                              │  │                            │
                              │  │ Compute Score:             │
                              │  │ (AvgWait×0.4) +            │
                              │  │ (Criticality×0.4) -        │
                              │  │ (Rate×0.2)                 │
                              │  └────────────────────────────┘
                              │
                              ▼
                    ┌──────────────────┐
                    │  Dispatch        │
                    │  (Selected!)     │
                    └──────────────────┘
                              │
                              ▼
        ┌─────────────────────────────────────────────────┐
        │  ⚡ TRACK: WorkloadHandleDispatchedRequest()   │
        │  • Update AverageWaitTime (EMA)                │
        │  • DispatchedCount++                           │
        └─────────────────────────────────────────────────┘
                              │
                              ▼
                    ┌──────────────────┐
                    │  Backend         │
                    │  Processing      │
                    └──────────────────┘
                              │
                              ▼
        ┌─────────────────────────────────────────────────┐
        │  ⚡ TRACK: WorkloadHandleCompletedRequest()    │
        │  • ActiveRequests--                            │
        └─────────────────────────────────────────────────┘
                              │
                              ▼
                    ┌──────────────────┐
                    │  Response to     │
                    │  Client          │
                    └──────────────────┘
```

## Workload Metrics Evolution

```
Request 1 Arrives:
┌──────────────────────────────┐
│ TotalRequests:    1          │
│ ActiveRequests:   1          │
│ AverageWaitTime:  0s         │
│ DispatchedCount:  0          │
└──────────────────────────────┘
         │
         │ (waits 2.5s, then dispatched)
         ▼
┌──────────────────────────────┐
│ TotalRequests:    1          │
│ ActiveRequests:   1          │
│ AverageWaitTime:  2.5s ◄──── First dispatch: direct set
│ DispatchedCount:  1          │
└──────────────────────────────┘
         │
         │ (processing completes)
         ▼
┌──────────────────────────────┐
│ TotalRequests:    1          │
│ ActiveRequests:   0 ◄──────── Decremented
│ AverageWaitTime:  2.5s       │
│ DispatchedCount:  1          │
└──────────────────────────────┘

Request 2 Arrives:
┌──────────────────────────────┐
│ TotalRequests:    2 ◄──────── Incremented
│ ActiveRequests:   1 ◄──────── Incremented
│ AverageWaitTime:  2.5s       │
│ DispatchedCount:  1          │
└──────────────────────────────┘
         │
         │ (waits 1.8s, then dispatched)
         ▼
┌──────────────────────────────┐
│ TotalRequests:    2          │
│ ActiveRequests:   1          │
│ AverageWaitTime:  2.36s ◄──── EMA: 0.2×1.8 + 0.8×2.5
│ DispatchedCount:  2          │
└──────────────────────────────┘
```

## Priority Score Example

```
Three requests competing in queue:

┌─────────────────────────────────────────────────────────────┐
│ Request A: "batch-job"                                      │
│ • Criticality: 4                                            │
│ • AvgWaitTime: 2.36s  → normalized: 0.039                   │
│ • RequestRate: 0.033  → normalized: 0.00033                 │
│ • Score: (0.039×0.4) + (0.8×0.4) - (0.00033×0.2) = 0.335   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Request B: "interactive-user"                               │
│ • Criticality: 5                                            │
│ • AvgWaitTime: 0.8s   → normalized: 0.013                   │
│ • RequestRate: 2.5    → normalized: 0.025                   │
│ • Score: (0.013×0.4) + (1.0×0.4) - (0.025×0.2) = 0.400 ⭐   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Request C: "low-priority"                                   │
│ • Criticality: 2                                            │
│ • AvgWaitTime: 15.0s  → normalized: 0.25 (anti-starvation!) │
│ • RequestRate: 0.1    → normalized: 0.001                   │
│ • Score: (0.25×0.4) + (0.4×0.4) - (0.001×0.2) = 0.260      │
└─────────────────────────────────────────────────────────────┘

Dispatch Order: B → A → C
```

## Key Components

```
┌─────────────────────────────────────────────────────────────┐
│ WorkloadRegistry                                            │
│ ├─ sync.Map: workloadID → WorkloadMetrics                   │
│ ├─ Cleanup goroutine (every 60s)                            │
│ └─ Methods:                                                 │
│    • IncrementActive()    - Track new request              │
│    • RecordDispatch()     - Update EMA wait time           │
│    • DecrementActive()    - Track completion               │
│    • GetMetrics()         - Read for scoring               │
│    • GetRequestRate()     - Read for scoring               │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ WorkloadMetrics (per workload)                              │
│ ├─ TotalRequests         - Lifetime counter                 │
│ ├─ ActiveRequests        - Currently in-flight              │
│ ├─ AverageWaitTime       - EMA of wait times                │
│ ├─ DispatchedCount       - Total dispatched                 │
│ ├─ RequestRate           - Requests per second              │
│ └─ sync.RWMutex          - Thread safety                    │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ WorkloadAwarePolicy                                         │
│ └─ computeScore()                                           │
│    ├─ Read workload metrics from registry                   │
│    ├─ Normalize values to [0,1]                             │
│    └─ Apply weighted formula                                │
└─────────────────────────────────────────────────────────────┘
```

## EMA Formula

```
Exponential Moving Average (α = 0.2):

First dispatch:
  AverageWaitTime = currentWaitTime

Subsequent dispatches:
  AverageWaitTime = (α × currentWaitTime) + ((1-α) × previousAverage)
                  = (0.2 × current) + (0.8 × previous)

Example:
  Previous: 2.5s
  Current:  1.8s
  New Avg:  (0.2 × 1.8) + (0.8 × 2.5) = 0.36 + 2.0 = 2.36s
```

## Thread Safety

```
Multiple concurrent requests:

Request 1 ──┐
Request 2 ──┼──► WorkloadRegistry (sync.Map)
Request 3 ──┘         │
                      ├──► WorkloadMetrics["job-123"] (RWMutex)
                      ├──► WorkloadMetrics["user-456"] (RWMutex)
                      └──► WorkloadMetrics["batch-789"] (RWMutex)

• sync.Map: Thread-safe concurrent access
• RWMutex per workload: Protects individual metrics
• Read locks for scoring (non-blocking)
• Write locks for updates (brief)