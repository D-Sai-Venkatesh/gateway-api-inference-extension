# Workload Registry and Request Interaction Diagram

## Complete Request Lifecycle with Workload Tracking

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           CLIENT REQUEST ARRIVES                                 │
│                     Headers: X-Workload-Context: {                              │
│                       "workload_id": "batch-job-123",                           │
│                       "criticality": 4                                          │
│                     }                                                           │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    STEP 1: REQUEST HEADER PROCESSING                            │
│                    handlers.HandleRequestHeaders()                              │
│                    File: pkg/epp/handlers/request.go:59                         │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ├─► Set RequestReceivedTimestamp = time.Now()
                                      │
                                      ├─► Extract headers into RequestContext
                                      │
                                      ├─► Call extractWorkloadContext()
                                      │   │
                                      │   ├─► Parse X-Workload-Context JSON
                                      │   ├─► Validate WorkloadID (generate if missing)
                                      │   ├─► Clamp Criticality to [1-5]
                                      │   └─► Return WorkloadContext struct
                                      │
                                      ├─► Store in RequestContext.WorkloadContext
                                      │
                                      └─► Copy to Request.Metadata map
                                          (workload_id, criticality)
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    STEP 2: ADMISSION CONTROL                                    │
│                    Director.HandleRequest()                                     │
│                    File: pkg/epp/handlers/server.go:268                         │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ├─► AdmissionController.Admit()
                                      │   │
                                      │   └─► FlowControlAdmissionController
                                      │       File: pkg/epp/requestcontrol/admission.go:153
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    STEP 3: FLOW CONTROL ENQUEUE                                 │
│                    FlowController.EnqueueAndWait()                              │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ├─► Create flowControlRequest with:
                                      │   - requestID, fairnessID, priority
                                      │   - reqMetadata (contains workload_id, criticality)
                                      │   - requestByteSize, modelName, etc.
                                      │
                                      ├─► ⚡ WORKLOAD TRACKING POINT 1 ⚡
                                      │   datastore.WorkloadHandleNewRequest(workloadID)
                                      │   File: pkg/epp/handlers/server.go:241
                                      │   │
                                      │   └─► WorkloadRegistry.IncrementActive(workloadID)
                                      │       File: pkg/epp/datastore/workload_registry.go:95
                                      │       │
                                      │       ├─► Get or create WorkloadMetrics
                                      │       ├─► metrics.TotalRequests++
                                      │       ├─► metrics.ActiveRequests++
                                      │       ├─► Update LastRequestTime = now
                                      │       └─► Update RequestRate (sliding window)
                                      │
                                      ├─► FlowController finds/creates flow queue
                                      │   based on FlowKey{ID: fairnessID, Priority: priority}
                                      │
                                      └─► Add request to queue
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    STEP 4: QUEUE WAITING                                        │
│                    Request sits in priority queue                               │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      │   ┌─────────────────────────────────────┐
                                      │   │  CONCURRENT: Priority Evaluation    │
                                      │   │  WorkloadAwarePolicy.Less()         │
                                      │   │  File: workload_aware.go:141        │
                                      │   └─────────────────────────────────────┘
                                      │                    │
                                      │                    ├─► For each queue item comparison:
                                      │                    │
                                      │                    ├─► Extract from metadata:
                                      │                    │   - workload_id
                                      │                    │   - criticality
                                      │                    │
                                      │                    ├─► ⚡ WORKLOAD METRICS READ ⚡
                                      │                    │   registry.GetMetrics(workloadID)
                                      │                    │   │
                                      │                    │   └─► Returns WorkloadMetrics:
                                      │                    │       - AverageWaitTime (EMA)
                                      │                    │       - ActiveRequests
                                      │                    │       - TotalRequests
                                      │                    │       - DispatchedCount
                                      │                    │
                                      │                    ├─► registry.GetRequestRate(workloadID)
                                      │                    │   │
                                      │                    │   └─► Calculate: TotalRequests / WindowDuration
                                      │                    │
                                      │                    ├─► Compute Priority Score:
                                      │                    │   score = (avgWaitTime × 0.4) +
                                      │                    │           (criticality × 0.4) -
                                      │                    │           (requestRate × 0.2)
                                      │                    │
                                      │                    └─► Return comparison result
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    STEP 5: REQUEST DISPATCH                                     │
│                    FlowController selects highest priority item                 │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ├─► Remove from queue
                                      │
                                      ├─► Return QueueOutcomeDispatched
                                      │
                                      └─► Control returns to Admit()
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    STEP 6: POST-DISPATCH TRACKING                               │
│                    Director.HandleRequest() continues                           │
│                    File: pkg/epp/handlers/server.go:268                         │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ├─► ⚡ WORKLOAD TRACKING POINT 2 ⚡
                                      │   datastore.WorkloadHandleDispatchedRequest(
                                      │       workloadID, 
                                      │       waitTime = time.Since(RequestReceivedTimestamp)
                                      │   )
                                      │   │
                                      │   └─► WorkloadRegistry.RecordDispatch()
                                      │       File: pkg/epp/datastore/workload_registry.go:186
                                      │       │
                                      │       ├─► Get WorkloadMetrics
                                      │       │
                                      │       ├─► Initialize EMAAlpha = 0.2 if zero
                                      │       │
                                      │       ├─► Update Average Wait Time (EMA):
                                      │       │   if DispatchedCount == 0:
                                      │       │       AverageWaitTime = waitTime
                                      │       │   else:
                                      │       │       AverageWaitTime = 
                                      │       │           (α × waitTime) + 
                                      │       │           ((1-α) × PreviousAverage)
                                      │       │
                                      │       └─► metrics.DispatchedCount++
                                      │
                                      ├─► Select target pod (scheduling)
                                      │
                                      └─► Forward request to backend
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    STEP 7: REQUEST PROCESSING                                   │
│                    Backend model server processes request                       │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      │   (Request being processed by backend)
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                    STEP 8: RESPONSE COMPLETE                                    │
│                    Director.HandleResponseBodyComplete()                        │
│                    File: pkg/epp/handlers/server.go (response handler)          │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ├─► ⚡ WORKLOAD TRACKING POINT 3 ⚡
                                      │   datastore.WorkloadHandleCompletedRequest(workloadID)
                                      │   │
                                      │   └─► WorkloadRegistry.DecrementActive(workloadID)
                                      │       File: pkg/epp/datastore/workload_registry.go:127
                                      │       │
                                      │       ├─► Get WorkloadMetrics
                                      │       ├─► metrics.ActiveRequests--
                                      │       └─► Update LastRequestTime = now
                                      │
                                      └─► Return response to client
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           RESPONSE SENT TO CLIENT                               │
└─────────────────────────────────────────────────────────────────────────────────┘


═══════════════════════════════════════════════════════════════════════════════════
                        BACKGROUND: WORKLOAD REGISTRY CLEANUP
═══════════════════════════════════════════════════════════════════════════════════

┌─────────────────────────────────────────────────────────────────────────────────┐
│                    CLEANUP GOROUTINE (Every 60 seconds)                         │
│                    WorkloadRegistry.cleanupLoop()                               │
│                    File: pkg/epp/datastore/workload_registry.go:215             │
└─────────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ├─► For each workload in registry:
                                      │   │
                                      │   ├─► Check if inactive:
                                      │   │   - ActiveRequests == 0
                                      │   │   - time.Since(LastRequestTime) > InactivityThreshold
                                      │   │
                                      │   └─► If inactive: Delete from registry
                                      │
                                      └─► Sleep until next cleanup cycle


═══════════════════════════════════════════════════════════════════════════════════
                        WORKLOAD METRICS STATE TRANSITIONS
═══════════════════════════════════════════════════════════════════════════════════

Initial State (Workload "batch-job-123" first request):
┌────────────────────────────────────────────────────────────────────┐
│ WorkloadMetrics {                                                  │
│   WorkloadID: "batch-job-123"                                      │
│   TotalRequests: 0                                                 │
│   ActiveRequests: 0                                                │
│   AverageWaitTime: 0                                               │
│   DispatchedCount: 0                                               │
│   RequestRate: 0.0                                                 │
│   LastRequestTime: <zero>                                          │
│   WindowStart: <zero>                                              │
│   EMAAlpha: 0.0                                                    │
│ }                                                                  │
└────────────────────────────────────────────────────────────────────┘
                          │
                          │ WorkloadHandleNewRequest()
                          ▼
After Enqueue:
┌────────────────────────────────────────────────────────────────────┐
│ WorkloadMetrics {                                                  │
│   WorkloadID: "batch-job-123"                                      │
│   TotalRequests: 1          ◄── Incremented                        │
│   ActiveRequests: 1         ◄── Incremented                        │
│   AverageWaitTime: 0        ◄── Not yet updated                    │
│   DispatchedCount: 0                                               │
│   RequestRate: 0.016        ◄── 1 req / 60s window                 │
│   LastRequestTime: T0       ◄── Updated                            │
│   WindowStart: T0           ◄── Initialized                        │
│   EMAAlpha: 0.0                                                    │
│ }                                                                  │
└────────────────────────────────────────────────────────────────────┘
                          │
                          │ (Wait in queue: 2.5 seconds)
                          │
                          │ WorkloadHandleDispatchedRequest(waitTime=2.5s)
                          ▼
After Dispatch:
┌────────────────────────────────────────────────────────────────────┐
│ WorkloadMetrics {                                                  │
│   WorkloadID: "batch-job-123"                                      │
│   TotalRequests: 1                                                 │
│   ActiveRequests: 1         ◄── Still active (processing)          │
│   AverageWaitTime: 2.5s     ◄── First dispatch: direct assignment  │
│   DispatchedCount: 1        ◄── Incremented                        │
│   RequestRate: 0.016                                               │
│   LastRequestTime: T0                                              │
│   WindowStart: T0                                                  │
│   EMAAlpha: 0.2             ◄── Initialized                        │
│ }                                                                  │
└────────────────────────────────────────────────────────────────────┘
                          │
                          │ (Backend processing: 5 seconds)
                          │
                          │ WorkloadHandleCompletedRequest()
                          ▼
After Completion:
┌────────────────────────────────────────────────────────────────────┐
│ WorkloadMetrics {                                                  │
│   WorkloadID: "batch-job-123"                                      │
│   TotalRequests: 1                                                 │
│   ActiveRequests: 0         ◄── Decremented                        │
│   AverageWaitTime: 2.5s                                            │
│   DispatchedCount: 1                                               │
│   RequestRate: 0.016                                               │
│   LastRequestTime: T0+7.5s  ◄── Updated                            │
│   WindowStart: T0                                                  │
│   EMAAlpha: 0.2                                                    │
│ }                                                                  │
└────────────────────────────────────────────────────────────────────┘
                          │
                          │ (Second request arrives after 3s)
                          │ WorkloadHandleNewRequest()
                          ▼
Second Request Enqueued:
┌────────────────────────────────────────────────────────────────────┐
│ WorkloadMetrics {                                                  │
│   WorkloadID: "batch-job-123"                                      │
│   TotalRequests: 2          ◄── Incremented                        │
│   ActiveRequests: 1         ◄── Incremented                        │
│   AverageWaitTime: 2.5s                                            │
│   DispatchedCount: 1                                               │
│   RequestRate: 0.033        ◄── 2 reqs / 60s window                │
│   LastRequestTime: T0+10.5s ◄── Updated                            │
│   WindowStart: T0                                                  │
│   EMAAlpha: 0.2                                                    │
│ }                                                                  │
└────────────────────────────────────────────────────────────────────┘
                          │
                          │ (Wait in queue: 1.8 seconds)
                          │
                          │ WorkloadHandleDispatchedRequest(waitTime=1.8s)
                          ▼
Second Request Dispatched (EMA Update):
┌────────────────────────────────────────────────────────────────────┐
│ WorkloadMetrics {                                                  │
│   WorkloadID: "batch-job-123"                                      │
│   TotalRequests: 2                                                 │
│   ActiveRequests: 1                                                │
│   AverageWaitTime: 2.36s    ◄── EMA: 0.2×1.8 + 0.8×2.5 = 2.36s    │
│   DispatchedCount: 2        ◄── Incremented                        │
│   RequestRate: 0.033                                               │
│   LastRequestTime: T0+10.5s                                        │
│   WindowStart: T0                                                  │
│   EMAAlpha: 0.2                                                    │
│ }                                                                  │
└────────────────────────────────────────────────────────────────────┘


═══════════════════════════════════════════════════════════════════════════════════
                        PRIORITY SCORE CALCULATION EXAMPLE
═══════════════════════════════════════════════════════════════════════════════════

Scenario: Three requests in queue, WorkloadAwarePolicy.Less() comparing them

Request A: workload_id="batch-job-123", criticality=4
  ├─► GetMetrics("batch-job-123"):
  │   - AverageWaitTime: 2.36s
  │   - RequestRate: 0.033 req/s
  │
  ├─► Normalize:
  │   - normalizedWait = min(2.36/60.0, 1.0) = 0.039
  │   - normalizedCrit = 4/5.0 = 0.8
  │   - normalizedRate = min(0.033/100.0, 1.0) = 0.00033
  │
  └─► Score = (0.039 × 0.4) + (0.8 × 0.4) - (0.00033 × 0.2)
            = 0.0156 + 0.32 - 0.000066
            = 0.335534

Request B: workload_id="interactive-user-456", criticality=5
  ├─► GetMetrics("interactive-user-456"):
  │   - AverageWaitTime: 0.8s (faster workload)
  │   - RequestRate: 2.5 req/s (high rate)
  │
  ├─► Normalize:
  │   - normalizedWait = min(0.8/60.0, 1.0) = 0.013
  │   - normalizedCrit = 5/5.0 = 1.0
  │   - normalizedRate = min(2.5/100.0, 1.0) = 0.025
  │
  └─► Score = (0.013 × 0.4) + (1.0 × 0.4) - (0.025 × 0.2)
            = 0.0052 + 0.4 - 0.005
            = 0.4002

Request C: workload_id="low-priority-789", criticality=2
  ├─► GetMetrics("low-priority-789"):
  │   - AverageWaitTime: 15.0s (starving!)
  │   - RequestRate: 0.1 req/s
  │
  ├─► Normalize:
  │   - normalizedWait = min(15.0/60.0, 1.0) = 0.25
  │   - normalizedCrit = 2/5.0 = 0.4
  │   - normalizedRate = min(0.1/100.0, 1.0) = 0.001
  │
  └─► Score = (0.25 × 0.4) + (0.4 × 0.4) - (0.001 × 0.2)
            = 0.1 + 0.16 - 0.0002
            = 0.2598

Priority Order (Highest to Lowest):
  1. Request B: 0.4002 (High criticality, despite high rate)
  2. Request A: 0.3355 (Medium-high criticality, low rate)
  3. Request C: 0.2598 (Low criticality, but anti-starvation helps)


═══════════════════════════════════════════════════════════════════════════════════
                        KEY DATA STRUCTURES
═══════════════════════════════════════════════════════════════════════════════════

WorkloadContext (from headers):
┌────────────────────────────────────────────────────────────────────┐
│ type WorkloadContext struct {                                      │
│     WorkloadID  string `json:"workload_id"`                        │
│     Criticality int    `json:"criticality"` // 1-5                 │
│ }                                                                  │
└────────────────────────────────────────────────────────────────────┘

WorkloadMetrics (in registry):
┌────────────────────────────────────────────────────────────────────┐
│ type WorkloadMetrics struct {                                      │
│     WorkloadID            string                                   │
│     TotalRequests         int64                                    │
│     ActiveRequests        int64                                    │
│     AverageWaitTime       time.Duration  // EMA                    │
│     DispatchedCount       int64                                    │
│     RequestRate           float64        // req/sec                │
│     LastRequestTime       time.Time                                │
│     WindowStart           time.Time                                │
│     EMAAlpha              float64        // 0.2                    │
│     mu                    sync.RWMutex                             │
│ }                                                                  │
└────────────────────────────────────────────────────────────────────┘

WorkloadRegistry:
┌────────────────────────────────────────────────────────────────────┐
│ type WorkloadRegistry struct {                                     │
│     workloads            sync.Map  // workloadID → *WorkloadMetrics│
│     inactivityThreshold  time.Duration                             │
│     cleanupInterval      time.Duration                             │
│     ctx                  context.Context                           │
│     cancel               context.CancelFunc                        │
│ }                                                                  │
└────────────────────────────────────────────────────────────────────┘


═══════════════════════════════════════════════════════════════════════════════════
                        THREAD SAFETY & CONCURRENCY
═══════════════════════════════════════════════════════════════════════════════════

1. WorkloadRegistry.workloads: sync.Map
   - Thread-safe concurrent map
   - Multiple goroutines can read/write simultaneously

2. WorkloadMetrics.mu: sync.RWMutex
   - Protects individual workload metrics
   - Read lock for GetMetrics(), GetRequestRate()
   - Write lock for IncrementActive(), DecrementActive(), RecordDispatch()

3. Cleanup goroutine:
   - Runs independently every 60 seconds
   - Uses sync.Map.Range() for safe iteration
   - Deletes inactive workloads atomically

4. Priority evaluation:
   - Multiple WorkloadAwarePolicy.Less() calls concurrent
   - Read-only access to metrics (RLock)
   - No blocking of request processing


═══════════════════════════════════════════════════════════════════════════════════
                        ERROR HANDLING & EDGE CASES
═══════════════════════════════════════════════════════════════════════════════════

1. Missing X-Workload-Context header:
   └─► Generate unique workload ID: "auto-" + UUID
       Prevents single requests from being penalized by rate limiting

2. Invalid JSON in header:
   └─► Generate unique workload ID: "auto-" + UUID
       Ensures request can still be processed

3. Empty workload_id in JSON:
   └─► Generate unique workload ID: "auto-" + UUID

4. Criticality out of range:
   └─► Clamp to [1, 5] range
       - If < 1: Set to 1
       - If > 5: Set to 5

5. Workload not found in registry during scoring:
   └─► Use default values:
       - AverageWaitTime: 0.0
       - RequestRate: 0.0
       - Criticality: from metadata or default 3

6. Nil WorkloadRegistry in policy:
   └─► Policy still works with default values
       Allows conformance tests without full setup

7. Context cancellation during queue wait:
   └─► DecrementActive() called via cleanup
       Ensures metrics stay consistent