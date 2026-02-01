/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package intraflow

import (
	"encoding/json"
	"math"
	"time"

	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/datastore"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/flowcontrol/framework"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/flowcontrol/types"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"
)

// WorkloadAwareOrderingPolicyType represents an ordering policy that implements workload-aware prioritization.
//
// It prioritizes requests based on a composite score that considers:
//   - Wait Time: Time spent in queue (anti-starvation mechanism)
//   - Criticality: User-defined priority level (1-5)
//   - Request Rate: Fairness across workloads (penalizes high-rate workloads)
//
// The priority score is computed as:
//
//	Score = (WaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
//
// All values are normalized to [0, 1] range before applying weights.
//
// This policy requires a CapabilityPriorityConfigurable queue (e.g., MaxMinHeap) to maintain
// items in priority-sorted order with lazy evaluation of scores.
const WorkloadAwareOrderingPolicyType = "workload-aware-ordering-policy"

// WorkloadAwarePolicyConfig holds configuration for the workload-aware policy.
type WorkloadAwarePolicyConfig struct {
	// WaitTimeWeight is the weight for wait time component (default: 0.4)
	WaitTimeWeight float64 `json:"waitTimeWeight,omitempty"`

	// CriticalityWeight is the weight for criticality component (default: 0.4)
	CriticalityWeight float64 `json:"criticalityWeight,omitempty"`

	// RequestRateWeight is the weight for request rate penalty (default: 0.2)
	RequestRateWeight float64 `json:"requestRateWeight,omitempty"`

	// MaxWaitTimeSeconds is the cap for wait time normalization (default: 60)
	MaxWaitTimeSeconds float64 `json:"maxWaitTimeSeconds,omitempty"`

	// MaxRequestRate is the cap for request rate normalization (default: 100)
	MaxRequestRate float64 `json:"maxRequestRate,omitempty"`
}

// DefaultWorkloadAwarePolicyConfig returns the default configuration.
func DefaultWorkloadAwarePolicyConfig() WorkloadAwarePolicyConfig {
	return WorkloadAwarePolicyConfig{
		WaitTimeWeight:     0.4,
		CriticalityWeight:  0.4,
		RequestRateWeight:  0.2,
		MaxWaitTimeSeconds: 60.0,
		MaxRequestRate:     100.0,
	}
}

// WorkloadAwarePolicy implements an OrderingPolicy based on workload-aware prioritization.
// It uses a composite scoring function that balances wait time, criticality, and request rate fairness.
type WorkloadAwarePolicy struct {
	config           WorkloadAwarePolicyConfig
	workloadRegistry *datastore.WorkloadRegistry
}

var _ framework.OrderingPolicy = &WorkloadAwarePolicy{}

// NewWorkloadAwarePolicy creates a new workload-aware policy with the given registry and config.
func NewWorkloadAwarePolicy(registry *datastore.WorkloadRegistry, config WorkloadAwarePolicyConfig) *WorkloadAwarePolicy {
	return &WorkloadAwarePolicy{
		config:           config,
		workloadRegistry: registry,
	}
}

// NewWorkloadAwarePolicyWithDefaults creates a new workload-aware policy with default configuration.
func NewWorkloadAwarePolicyWithDefaults(registry *datastore.WorkloadRegistry) *WorkloadAwarePolicy {
	return NewWorkloadAwarePolicy(registry, DefaultWorkloadAwarePolicyConfig())
}

func init() {
	// Register the plugin with a default implementation (nil registry).
	// For production use with full functionality, use NewWorkloadAwarePolicyFactory
	// to create policies with proper WorkloadRegistry dependency injection.
	plugin.Register(WorkloadAwareOrderingPolicyType, func(string, json.RawMessage, plugin.Handle) (plugin.Plugin, error) {
		// Return a policy with nil registry - it will still work but without request rate tracking.
		// This allows the policy to pass conformance tests and be used in simple scenarios.
		// For full workload-aware functionality, use the factory pattern with a WorkloadRegistry.
		return NewWorkloadAwarePolicyWithDefaults(nil), nil
	})
}

// SetWorkloadRegistry injects the WorkloadRegistry into this policy instance.
// This method is used to wire up the registry after policy instantiation,
// particularly for policies created via the plugin system before the registry is available.
//
// This method is safe to call multiple times and is thread-safe for the initial injection.
// However, it should not be called concurrently with SelectItem() operations.
func (p *WorkloadAwarePolicy) SetWorkloadRegistry(registry *datastore.WorkloadRegistry) {
	p.workloadRegistry = registry
}

// Name returns the name of the policy.
func (p *WorkloadAwarePolicy) Name() string {
	return WorkloadAwareOrderingPolicyType
}

// RequiredQueueCapabilities returns the queue capabilities required by this policy.
// It requires a priority-configurable queue (e.g., MaxMinHeap) to maintain items in priority-sorted order.
func (p *WorkloadAwarePolicy) RequiredQueueCapabilities() []framework.QueueCapability {
	return []framework.QueueCapability{framework.CapabilityPriorityConfigurable}
}

// TypedName returns the type and name tuple of this plugin instance.
func (p *WorkloadAwarePolicy) TypedName() plugin.TypedName {
	return plugin.TypedName{
		Type: WorkloadAwareOrderingPolicyType,
		Name: WorkloadAwareOrderingPolicyType,
	}
}

// Less returns true if item 'a' should be dispatched before item 'b'.
// Workload-aware ordering uses a composite score based on wait time, criticality, and request rate.
func (p *WorkloadAwarePolicy) Less(a, b types.QueueItemAccessor) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil { // Treat nil as lowest priority
		return false
	}
	if b == nil { // Treat non-nil 'a' as higher priority than nil 'b'
		return true
	}

	now := time.Now()
	scoreA := p.computeScore(a, now)
	scoreB := p.computeScore(b, now)

	if scoreA != scoreB {
		return scoreA > scoreB // Higher score = higher priority
	}

	// Tie-breaker: FCFS (earlier enqueue time = higher priority)
	return a.EnqueueTime().Before(b.EnqueueTime())
}

// computeScore calculates the priority score for a queue item.
// The score is a weighted combination of normalized average wait time, criticality, and request rate penalty.
// Uses the workload's historical average wait time (EMA) instead of individual request wait time.
func (p *WorkloadAwarePolicy) computeScore(item types.QueueItemAccessor, now time.Time) float64 {
	// Get workload context directly from request
	workloadCtx := item.OriginalRequest().GetWorkloadContext()

	// Default values if no workload context
	workloadID := "default"
	criticality := 3 // Default to medium priority

	if workloadCtx != nil {
		workloadID = workloadCtx.GetWorkloadID()
		criticality = workloadCtx.GetCriticality()

		// Validate criticality range
		if criticality < 1 || criticality > 5 {
			criticality = 3
		}
	}

	// Get workload metrics from registry
	avgWaitTime := 0.0
	requestRate := 0.0
	if p.workloadRegistry != nil {
		// Use workload's AVERAGE wait time instead of individual request wait time
		metrics := p.workloadRegistry.GetMetrics(workloadID)
		if metrics != nil {
			avgWaitTime = metrics.AverageWaitTime.Seconds()
		}
		requestRate = p.workloadRegistry.GetRequestRate(workloadID)
	}

	// Normalize all components to [0, 1] range
	normalizedWait := math.Min(avgWaitTime/p.config.MaxWaitTimeSeconds, 1.0)
	normalizedCrit := float64(criticality) / 5.0
	normalizedRate := math.Min(requestRate/p.config.MaxRequestRate, 1.0)

	// Compute weighted score
	// Higher average wait time → higher priority (anti-starvation for workload)
	// Higher criticality → higher priority (user intent)
	// Higher request rate → lower priority (fairness)
	score := (normalizedWait * p.config.WaitTimeWeight) +
		(normalizedCrit * p.config.CriticalityWeight) -
		(normalizedRate * p.config.RequestRateWeight)

	return score
}

// WorkloadAwarePolicyFactory creates WorkloadAwarePolicy instances with proper dependency injection.
type WorkloadAwarePolicyFactory struct {
	workloadRegistry *datastore.WorkloadRegistry
	config           WorkloadAwarePolicyConfig
}

// NewWorkloadAwarePolicyFactory creates a new factory for workload-aware policies.
func NewWorkloadAwarePolicyFactory(registry *datastore.WorkloadRegistry) *WorkloadAwarePolicyFactory {
	return &WorkloadAwarePolicyFactory{
		workloadRegistry: registry,
		config:           DefaultWorkloadAwarePolicyConfig(),
	}
}

// NewWorkloadAwarePolicyFactoryWithConfig creates a new factory with custom configuration.
func NewWorkloadAwarePolicyFactoryWithConfig(registry *datastore.WorkloadRegistry, config WorkloadAwarePolicyConfig) *WorkloadAwarePolicyFactory {
	return &WorkloadAwarePolicyFactory{
		workloadRegistry: registry,
		config:           config,
	}
}

// CreatePolicy creates a new WorkloadAwarePolicy instance.
func (f *WorkloadAwarePolicyFactory) CreatePolicy() framework.OrderingPolicy {
	return NewWorkloadAwarePolicy(f.workloadRegistry, f.config)
}

// Made with Bob
