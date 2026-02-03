# Workload-Aware Routing - Execution Phases

## Overview

This document outlines the execution phases for the workload-aware routing implementation in the Gateway API Inference Extension. It tracks completed work and defines remaining phases.

**Last Updated:** 2026-02-01  
**Status:** Phase 6 Complete - Ready for Phase 7

---

## ✅ COMPLETED PHASES

### Phase 1: Foundation & Data Structures ✅
**Status:** COMPLETE  
**Completion Date:** 2026-01-24

#### Deliverables
- ✅ [`WorkloadRegistry`](../../pkg/epp/datastore/workload_registry.go) implementation
- ✅ [`WorkloadMetrics`](../../pkg/epp/datastore/workload_registry.go) structure with EMA-based average wait time
- ✅ [`WorkloadContext`](../../pkg/epp/datastore/types.go) structure
- ✅ Thread-safe operations with `sync.Map` and `sync.RWMutex`
- ✅ Comprehensive unit tests with >80% coverage
- ✅ Race condition testing passed

#### Key Files
- `pkg/epp/datastore/workload_registry.go`
- `pkg/epp/datastore/workload_registry_test.go`
- `pkg/epp/datastore/types.go`

---

### Phase 2: Workload Context Extraction ✅
**Status:** COMPLETE  
**Completion Date:** 2026-01-24

#### Deliverables
- ✅ Header parsing in [`HandleRequestHeaders()`](../../pkg/epp/handlers/server.go)
- ✅ JSON validation and error handling
- ✅ Default workload handling (criticality=3)
- ✅ Criticality range validation (1-5)
- ✅ Integration with RequestContext

#### Key Features
- Header name: `X-Workload-Context`
- JSON format: `{"workload_id": "string", "criticality": 1-5}`
- Graceful degradation for missing/invalid headers

---

### Phase 3: Registry Integration ✅
**Status:** COMPLETE  
**Completion Date:** 2026-01-25

#### Deliverables
- ✅ WorkloadRegistry integrated into [`Datastore`](../../pkg/epp/datastore/datastore.go)
- ✅ Request lifecycle tracking (increment on enqueue, decrement on completion)
- ✅ Sliding window request rate calculation
- ✅ Cleanup of inactive workloads
- ✅ Thread-safe concurrent access

#### Integration Points
- `IncrementActive()` called in `HandleRequestHeaders()`
- `DecrementActive()` called in request completion defer
- Registry accessible via `Datastore.GetWorkloadRegistry()`

---

### Phase 4: Flow Control & Priority Logic ✅
**Status:** COMPLETE  
**Completion Date:** 2026-01-26

#### Deliverables
- ✅ [`WorkloadAwarePolicy`](../../pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go) implementation
- ✅ [`WorkloadAwareComparator`](../../pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go) with priority scoring
- ✅ Integration with existing MaxMinHeap queue
- ✅ Priority formula: `(AvgWaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)`
- ✅ Exponential Moving Average (EMA) for wait time calculation
- ✅ Policy registration and factory pattern

#### Key Algorithm
```go
Priority Score = (AvgWaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
```

**Note:** Uses average wait time (EMA) instead of individual request wait time for better stability and fairness.

---

### Phase 4.5: Complete WorkloadRegistry Injection ✅
**Status:** COMPLETE  
**Completion Date:** 2026-01-26

#### Deliverables
- ✅ Registry passed to all policy instances via factory
- ✅ [`NewWorkloadAwarePolicy()`](../../pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go) constructor with registry parameter
- ✅ Integration in [`ShardProcessor`](../../pkg/epp/flowcontrol/controller/internal/processor.go)
- ✅ Registry available in comparator for live metrics

---

### Phase 4.6: Handle Dynamic Priority Band Creation ✅
**Status:** COMPLETE  
**Completion Date:** 2026-01-26

#### Deliverables
- ✅ Dynamic priority bands receive registry reference
- ✅ [`createPriorityBand()`](../../pkg/epp/flowcontrol/controller/internal/processor.go) updated to pass registry
- ✅ All policy instances have access to live workload metrics

---

### Phase 4.7: Complete Data Flow ✅
**Status:** COMPLETE (Later Refactored in Phase 6)  
**Completion Date:** 2026-01-26

#### Original Implementation
- ✅ WorkloadContext copied to `Request.Metadata` map
- ✅ Metadata accessible in comparator for priority calculation
- ✅ End-to-end data flow verified

**Note:** This approach was later refactored in Phase 6 for better type safety.

---

### Phase 5: End-to-End Testing & Configuration ✅
**Status:** COMPLETE  
**Completion Date:** 2026-01-26

#### Deliverables
- ✅ Configuration file created: [`test/testdata/inferencepool-e2e.yaml`](../../test/testdata/inferencepool-e2e.yaml)
- ✅ WorkloadAware policy properly configured
- ✅ IntraFlowDispatch section added
- ✅ Build verification passed
- ✅ Ready for manual testing

#### Configuration Structure
```yaml
intraFlowDispatch:
  policy: WorkloadAware
  config:
    weights:
      waitTime: 0.4
      criticality: 0.4
      requestRate: 0.2
```

---

### Phase 6: Workload Context Refactoring ✅
**Status:** COMPLETE  
**Completion Date:** 2026-02-01

#### Deliverables
- ✅ **Option A Implementation:** Keep Registry for Metrics
- ✅ [`WorkloadContext` interface](../../pkg/epp/flowcontrol/types/request.go) in flowcontrol/types
- ✅ [`FlowControlRequest.GetWorkloadContext()`](../../pkg/epp/flowcontrol/types/request.go) method
- ✅ Concrete implementation in [`admission.go`](../../pkg/epp/requestcontrol/admission.go)
- ✅ Type-safe access in [`WorkloadAwarePolicy`](../../pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go)
- ✅ Removed metadata map copying
- ✅ All mocks updated
- ✅ Build verification passed

#### Key Benefits
- ✅ Eliminated runtime type casting from `map[string]any`
- ✅ Compile-time type safety
- ✅ Cleaner architecture (no dependency from flowcontrol/types to datastore)
- ✅ Separation of concerns: workload identity (context) vs workload metrics (registry)

#### Architecture Decision
**Chosen:** Option A - Keep Registry for Metrics
- WorkloadContext passed via interface for identity/criticality
- WorkloadRegistry used for aggregated metrics (EMA, request rates)
- Clean separation between request-level data and workload-level metrics

---

## 🔄 CURRENT PHASE

### Phase 7: Performance & Load Testing 🔄
**Status:** PENDING  
**Priority:** HIGH  
**Estimated Duration:** 2-3 days

#### Objectives
1. Validate performance characteristics under load
2. Identify bottlenecks and optimization opportunities
3. Ensure system stability under stress
4. Verify no memory leaks or race conditions
5. Establish performance baselines

---

## 📋 REMAINING PHASES

### Phase 7.1: Benchmark Tests
**Status:** NOT STARTED

#### Tasks
- [ ] Create benchmark for score computation
- [ ] Benchmark heap operations (Add, PeekHead, Remove)
- [ ] Benchmark WorkloadRegistry operations
- [ ] Profile with pprof (CPU and memory)
- [ ] Analyze and optimize hot paths

#### Performance Targets
- Score computation: <1µs per call
- Heap add: <10µs per operation
- Heap peek: <100ns per operation
- Registry operations: <500ns per call

#### Files to Create
- `pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware_bench_test.go`
- `pkg/epp/datastore/workload_registry_bench_test.go`

---

### Phase 7.2: Load Testing
**Status:** NOT STARTED

#### Tasks
- [ ] Baseline test (no workload context)
- [ ] Single workload test (with context)
- [ ] Mixed workload test (multiple priorities)
- [ ] Compare latency distributions (p50, p95, p99)
- [ ] Measure throughput impact
- [ ] Verify error rates remain low

#### Test Scenarios
1. **Baseline:** 10k requests, 100 concurrent, no workload headers
2. **Single Workload:** 10k requests, 100 concurrent, same workload
3. **Mixed Workload:** 10k requests, 100 concurrent, 3 workloads (low/med/high priority)

#### Performance Targets
- Throughput: >90% of baseline
- Latency overhead: <5ms p99
- Error rate: <0.1%

#### Tools
- `hey` or `wrk` for HTTP load testing
- Custom Go test harness for fine-grained control

---

### Phase 7.3: Stress Testing
**Status:** NOT STARTED

#### Tasks
- [ ] Concurrent workload registry updates (1000 goroutines)
- [ ] Sustained load test (1 hour, 100 req/s)
- [ ] Memory leak detection (24 hour run)
- [ ] Race condition testing under load
- [ ] Verify cleanup mechanisms work correctly

#### Test Scenarios
1. **Registry Stress:** 1000 goroutines × 10k operations each
2. **Sustained Load:** 1M requests over 1 hour
3. **Memory Leak:** 24 hour run with periodic heap snapshots

#### Performance Targets
- No race conditions detected
- Memory usage stabilizes (no unbounded growth)
- No goroutine leaks
- Cleanup removes inactive workloads within 5 minutes

#### Monitoring
- pprof heap profiles every 10 minutes
- Goroutine count tracking
- Memory usage graphs
- CPU utilization metrics

---

### Phase 7.4: Observability Validation
**Status:** NOT STARTED

#### Tasks
- [ ] Verify metrics are exposed correctly
- [ ] Test log output at various levels (debug, info, error)
- [ ] Validate workload context appears in traces (if applicable)
- [ ] Create sample Grafana dashboards
- [ ] Document metric meanings and usage

#### Metrics to Validate
- `workload_active_requests{workload_id}`
- `workload_total_requests{workload_id}`
- `workload_request_rate{workload_id}`
- `workload_avg_wait_time{workload_id}`
- `priority_score_distribution`

---

### Phase 8: Manual End-to-End Testing
**Status:** NOT STARTED  
**Priority:** HIGH

#### Test Scenarios

##### Scenario 1: Basic Priority Ordering
- Send low-priority request (criticality=1)
- Send high-priority request (criticality=5)
- **Expected:** High-priority completes first

##### Scenario 2: Wait Time Boost
- Send medium-priority request (criticality=2)
- Wait 30 seconds
- Send high-priority request (criticality=3)
- **Expected:** Older request may get priority boost

##### Scenario 3: Request Rate Fairness
- Flood with 50 requests from workload A (criticality=4)
- Send 1 request from workload B (criticality=4)
- **Expected:** Workload B gets fair treatment despite lower volume

##### Scenario 4: Default Workload Handling
- Send request without `X-Workload-Context` header
- **Expected:** Treated as default workload with criticality=3

##### Scenario 5: Invalid Header Handling
- Send request with malformed JSON in header
- **Expected:** Graceful degradation to default workload

---

### Phase 9: Documentation & Examples
**Status:** NOT STARTED  
**Priority:** MEDIUM

#### Tasks
- [ ] Update user-facing documentation
- [ ] Create usage examples
- [ ] Document configuration options
- [ ] Add troubleshooting guide
- [ ] Create architecture diagrams
- [ ] Write migration guide (if applicable)

#### Documentation Files
- `site-src/guides/workload-aware-routing.md`
- `site-src/api-types/workload-context.md`
- `docs/workload-aware-routing/USER_GUIDE.md`
- `docs/workload-aware-routing/TROUBLESHOOTING.md`

---

### Phase 10: Production Rollout
**Status:** NOT STARTED  
**Priority:** MEDIUM

#### Rollout Strategy

##### Stage 1: Development Environment
- Deploy to dev cluster
- Run all automated tests
- Manual validation with test workloads
- **Duration:** 1-2 days

##### Stage 2: Staging Environment
- Deploy with feature flag (disabled by default)
- Enable for synthetic test traffic
- Monitor metrics and logs
- Performance comparison with baseline
- **Duration:** 3-5 days

##### Stage 3: Production Canary
- Enable for 1% of traffic
- Monitor for 24 hours
- Check for errors, latency impact
- Gradually increase: 1% → 10% → 50% → 100%
- **Duration:** 1-2 weeks

##### Stage 4: Full Rollout
- Enable for all traffic
- Document usage for users
- Provide examples and best practices
- Monitor for issues
- **Duration:** Ongoing

---

## 📊 TESTING SUMMARY

### Completed Tests ✅
- [x] WorkloadRegistry unit tests
- [x] Header parsing unit tests
- [x] Score computation unit tests
- [x] Policy integration tests
- [x] Concurrent access tests
- [x] Race condition tests
- [x] Build verification

### Pending Tests 🔄
- [ ] Benchmark tests (score computation, heap operations)
- [ ] Load tests (baseline, single workload, mixed workload)
- [ ] Stress tests (registry, sustained load, memory leak)
- [ ] Manual E2E tests (5 scenarios)
- [ ] Observability validation

---

## 🎯 SUCCESS CRITERIA

### Functional Requirements ✅
- [x] Workload context extracted from headers
- [x] Priority scoring based on criticality, wait time, and request rate
- [x] Requests dispatched in priority order
- [x] Default workload handling for missing headers
- [x] Thread-safe concurrent operations

### Performance Requirements 🔄
- [ ] Throughput: >90% of baseline
- [ ] Latency overhead: <5ms p99
- [ ] Memory overhead: <10MB for 1000 active workloads
- [ ] CPU overhead: <10% increase
- [ ] No race conditions under load
- [ ] No memory leaks over 24h run

### Quality Requirements ✅
- [x] Code coverage >80%
- [x] All unit tests passing
- [x] No race conditions detected
- [x] Clean architecture with separation of concerns
- [x] Type-safe interfaces

---

## 🚀 NEXT STEPS

### Immediate Actions (Phase 7)
1. **Create benchmark tests** for score computation and heap operations
2. **Run load tests** with hey/wrk to measure throughput and latency
3. **Execute stress tests** to verify stability under sustained load
4. **Monitor for memory leaks** using pprof over extended runs
5. **Validate observability** metrics and logging

### After Phase 7
1. **Manual E2E testing** with real requests (Phase 8)
2. **Documentation updates** for users (Phase 9)
3. **Production rollout planning** (Phase 10)

---

## 📝 NOTES

### Design Decisions
- **EMA for Wait Time:** Using Exponential Moving Average instead of individual request wait time provides better stability and prevents gaming the system
- **Option A for Context:** Keeping WorkloadRegistry for metrics while passing context via interface provides clean separation of concerns
- **MaxMinHeap Reuse:** Leveraging existing queue infrastructure minimizes new code and risk

### Known Limitations
- Maximum 1000 concurrent workloads recommended (memory overhead)
- Sliding window fixed at 60 seconds (not configurable yet)
- Cleanup interval fixed at 5 minutes (not configurable yet)

### Future Enhancements
- Dynamic weight adjustment based on historical data
- SLA-based prioritization
- Per-workload quotas and rate limiting
- Advanced fairness algorithms (WFQ, DRR)
- Enhanced observability with Grafana dashboards

---

## 📚 REFERENCES

- [Original Implementation Plan](workload-aware-routing-plan.md)
- [Workload Context Refactoring Proposal](WORKLOAD_CONTEXT_REFACTORING_PROPOSAL.md)
- [Average Wait Time Implementation](AVERAGE_WAIT_TIME_IMPLEMENTATION_PLAN.md)
- [Phase 5 Implementation Summary](PHASE_5_IMPLEMENTATION_SUMMARY.md)
- [Phase 6 Refactoring Summary](PHASE_6_REFACTORING_SUMMARY.md)

---

**Document Version:** 1.0  
**Last Updated:** 2026-02-01  
**Status:** Phase 6 Complete, Phase 7 Pending Approval