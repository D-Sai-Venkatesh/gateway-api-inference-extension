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
	"testing"
	"time"

	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/datastore"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/flowcontrol/framework"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/flowcontrol/types"
)

// mockQueueItem implements types.QueueItemAccessor for testing
type mockQueueItem struct {
	enqueueTime     time.Time
	effectiveTTL    time.Duration
	handle          types.QueueItemHandle
	originalRequest *mockFlowControlRequest
}

func (m *mockQueueItem) OriginalRequest() types.FlowControlRequest {
	return m.originalRequest
}

func (m *mockQueueItem) EnqueueTime() time.Time {
	return m.enqueueTime
}

func (m *mockQueueItem) EffectiveTTL() time.Duration {
	return m.effectiveTTL
}

func (m *mockQueueItem) Handle() types.QueueItemHandle {
	return m.handle
}

func (m *mockQueueItem) SetHandle(handle types.QueueItemHandle) {
	m.handle = handle
}

// mockFlowControlRequest implements types.FlowControlRequest for testing
type mockFlowControlRequest struct {
	flowKey             types.FlowKey
	byteSize            uint64
	initialEffectiveTTL time.Duration
	id                  string
	metadata            map[string]any
	inferencePoolName   string
	modelName           string
	targetModelName     string
}

func (m *mockFlowControlRequest) FlowKey() types.FlowKey {
	return m.flowKey
}

func (m *mockFlowControlRequest) ByteSize() uint64 {
	return m.byteSize
}

func (m *mockFlowControlRequest) InitialEffectiveTTL() time.Duration {
	return m.initialEffectiveTTL
}

func (m *mockFlowControlRequest) ID() string {
	return m.id
}

func (m *mockFlowControlRequest) GetMetadata() map[string]any {
	return m.metadata
}

func (m *mockFlowControlRequest) InferencePoolName() string {
	return m.inferencePoolName
}

func (m *mockFlowControlRequest) ModelName() string {
	return m.modelName
}

func (m *mockFlowControlRequest) TargetModelName() string {
	return m.targetModelName
}

// Helper function to create a mock queue item with workload context
func createMockItem(workloadID string, criticality int, enqueueTime time.Time) *mockQueueItem {
	return &mockQueueItem{
		enqueueTime:  enqueueTime,
		effectiveTTL: 60 * time.Second,
		originalRequest: &mockFlowControlRequest{
			flowKey:  types.FlowKey{ID: "test-flow", Priority: 0},
			byteSize: 1024,
			id:       "test-request",
			metadata: map[string]any{
				"workload_id": workloadID,
				"criticality": criticality,
			},
		},
	}
}

func TestWorkloadAwarePolicy_Name(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	if policy.Name() != WorkloadAwareOrderingPolicyType {
		t.Errorf("Expected name %s, got %s", WorkloadAwareOrderingPolicyType, policy.Name())
	}
}

func TestWorkloadAwarePolicy_RequiredQueueCapabilities(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	caps := policy.RequiredQueueCapabilities()
	if len(caps) != 1 {
		t.Fatalf("Expected 1 capability, got %d", len(caps))
	}
	
	if caps[0] != framework.CapabilityPriorityConfigurable {
		t.Errorf("Expected CapabilityPriorityConfigurable, got %v", caps[0])
	}
}

func TestWorkloadAwarePolicy_Less_NilHandling(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	now := time.Now()
	item := createMockItem("workload-a", 3, now)
	
	tests := []struct {
		name     string
		a        types.QueueItemAccessor
		b        types.QueueItemAccessor
		expected bool
	}{
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: false,
		},
		{
			name:     "a is nil",
			a:        nil,
			b:        item,
			expected: false,
		},
		{
			name:     "b is nil",
			a:        item,
			b:        nil,
			expected: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := policy.Less(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestWorkloadAwarePolicy_Less_CriticalityOrdering(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	now := time.Now()
	
	// Create items with different criticality levels (same enqueue time, no request rate)
	lowPriority := createMockItem("workload-low", 1, now)
	mediumPriority := createMockItem("workload-medium", 3, now)
	highPriority := createMockItem("workload-high", 5, now)
	
	tests := []struct {
		name     string
		a        types.QueueItemAccessor
		b        types.QueueItemAccessor
		expected bool
		desc     string
	}{
		{
			name:     "high > medium",
			a:        highPriority,
			b:        mediumPriority,
			expected: true,
			desc:     "High criticality should have higher priority than medium",
		},
		{
			name:     "medium > low",
			a:        mediumPriority,
			b:        lowPriority,
			expected: true,
			desc:     "Medium criticality should have higher priority than low",
		},
		{
			name:     "high > low",
			a:        highPriority,
			b:        lowPriority,
			expected: true,
			desc:     "High criticality should have higher priority than low",
		},
		{
			name:     "low < high",
			a:        lowPriority,
			b:        highPriority,
			expected: false,
			desc:     "Low criticality should have lower priority than high",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := policy.Less(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("%s: Expected %v, got %v", tt.desc, tt.expected, result)
			}
		})
	}
}

func TestWorkloadAwarePolicy_Less_WaitTimeBoost(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	now := time.Now()
	
	// Create items with same criticality but different wait times
	oldRequest := createMockItem("workload-old", 3, now.Add(-30*time.Second))
	newRequest := createMockItem("workload-new", 3, now)
	
	// Old request should have higher priority due to wait time boost
	result := policy.Less(oldRequest, newRequest)
	if !result {
		t.Error("Older request should have higher priority due to wait time boost")
	}
	
	// Verify the reverse is false
	result = policy.Less(newRequest, oldRequest)
	if result {
		t.Error("Newer request should have lower priority than older request")
	}
}

func TestWorkloadAwarePolicy_Less_RequestRatePenalty(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	now := time.Now()
	
	// Simulate high request rate for workload-flood
	for i := 0; i < 50; i++ {
		registry.IncrementActive("workload-flood")
		registry.DecrementActive("workload-flood")
	}
	
	// Create items with same criticality and enqueue time
	floodWorkload := createMockItem("workload-flood", 4, now)
	fairWorkload := createMockItem("workload-fair", 4, now)
	
	// Fair workload should have higher priority despite same criticality
	// because flood workload has high request rate
	result := policy.Less(fairWorkload, floodWorkload)
	if !result {
		t.Error("Fair workload should have higher priority than flood workload due to request rate penalty")
	}
}

func TestWorkloadAwarePolicy_Less_CompositeScoring(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	now := time.Now()
	
	// Scenario: Low criticality but very old request vs high criticality new request
	// The wait time boost should eventually overcome the criticality difference
	veryOldLowPriority := createMockItem("workload-old-low", 1, now.Add(-60*time.Second))
	newHighPriority := createMockItem("workload-new-high", 5, now)
	
	// With default weights (wait=0.4, crit=0.4, rate=0.2):
	// Old low: (60/60)*0.4 + (1/5)*0.4 - 0 = 0.4 + 0.08 = 0.48
	// New high: (0/60)*0.4 + (5/5)*0.4 - 0 = 0 + 0.4 = 0.4
	// Old low should win
	result := policy.Less(veryOldLowPriority, newHighPriority)
	if !result {
		t.Error("Very old low-priority request should have higher priority than new high-priority request due to wait time boost")
	}
}

func TestWorkloadAwarePolicy_Less_TieBreaker(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	now := time.Now()
	
	// Create items with identical scores but different enqueue times
	earlier := createMockItem("workload-a", 3, now.Add(-1*time.Second))
	later := createMockItem("workload-b", 3, now)
	
	// Earlier enqueue time should win (FCFS tie-breaker)
	result := policy.Less(earlier, later)
	if !result {
		t.Error("Earlier enqueued request should win tie-breaker")
	}
}

func TestWorkloadAwarePolicy_ComputeScore_MissingMetadata(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	now := time.Now()
	
	// Create item with missing metadata
	item := &mockQueueItem{
		enqueueTime:  now,
		effectiveTTL: 60 * time.Second,
		originalRequest: &mockFlowControlRequest{
			flowKey:  types.FlowKey{ID: "test-flow", Priority: 0},
			byteSize: 1024,
			id:       "test-request",
			metadata: map[string]any{}, // Empty metadata
		},
	}
	
	// Should use default values (workload_id="default", criticality=3)
	score := policy.computeScore(item, now)
	
	// With defaults: wait=0, crit=3/5=0.6, rate=0
	// Score = 0*0.4 + 0.6*0.4 - 0*0.2 = 0.24
	expectedScore := 0.24
	if score != expectedScore {
		t.Errorf("Expected score %f for missing metadata, got %f", expectedScore, score)
	}
}

func TestWorkloadAwarePolicy_ComputeScore_InvalidCriticality(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	policy := NewWorkloadAwarePolicyWithDefaults(registry)
	
	now := time.Now()
	
	tests := []struct {
		name        string
		criticality int
		desc        string
	}{
		{
			name:        "criticality too low",
			criticality: 0,
			desc:        "Criticality 0 should default to 3",
		},
		{
			name:        "criticality negative",
			criticality: -1,
			desc:        "Negative criticality should default to 3",
		},
		{
			name:        "criticality too high",
			criticality: 6,
			desc:        "Criticality 6 should default to 3",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := createMockItem("workload-test", tt.criticality, now)
			score := policy.computeScore(item, now)
			
			// Should use default criticality=3
			// Score = 0*0.4 + (3/5)*0.4 - 0*0.2 = 0.24
			expectedScore := 0.24
			if score != expectedScore {
				t.Errorf("%s: Expected score %f, got %f", tt.desc, expectedScore, score)
			}
		})
	}
}

func TestWorkloadAwarePolicy_CustomConfig(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	
	// Create policy with custom weights (prioritize criticality more)
	config := WorkloadAwarePolicyConfig{
		WaitTimeWeight:     0.2,
		CriticalityWeight:  0.6,
		RequestRateWeight:  0.2,
		MaxWaitTimeSeconds: 60.0,
		MaxRequestRate:     100.0,
	}
	policy := NewWorkloadAwarePolicy(registry, config)
	
	now := time.Now()
	
	// With higher criticality weight, high-priority should win even with less wait time
	oldLowPriority := createMockItem("workload-old-low", 1, now.Add(-30*time.Second))
	newHighPriority := createMockItem("workload-new-high", 5, now)
	
	// Old low: (30/60)*0.2 + (1/5)*0.6 - 0 = 0.1 + 0.12 = 0.22
	// New high: (0/60)*0.2 + (5/5)*0.6 - 0 = 0 + 0.6 = 0.6
	// New high should win with higher criticality weight
	result := policy.Less(newHighPriority, oldLowPriority)
	if !result {
		t.Error("With higher criticality weight, new high-priority should win")
	}
}

func TestWorkloadAwarePolicyFactory(t *testing.T) {
	registry := datastore.NewWorkloadRegistry(60 * time.Second)
	
	t.Run("factory with defaults", func(t *testing.T) {
		factory := NewWorkloadAwarePolicyFactory(registry)
		policy := factory.CreatePolicy()
		
		if policy == nil {
			t.Fatal("Factory should create non-nil policy")
		}
		
		// Cast to concrete type to access Name()
		waPolicy, ok := policy.(*WorkloadAwarePolicy)
		if !ok {
			t.Fatal("Factory should create WorkloadAwarePolicy")
		}
		
		if waPolicy.Name() != WorkloadAwareOrderingPolicyType {
			t.Errorf("Expected policy name %s, got %s", WorkloadAwareOrderingPolicyType, waPolicy.Name())
		}
	})
	
	t.Run("factory with custom config", func(t *testing.T) {
		config := WorkloadAwarePolicyConfig{
			WaitTimeWeight:     0.5,
			CriticalityWeight:  0.3,
			RequestRateWeight:  0.2,
			MaxWaitTimeSeconds: 120.0,
			MaxRequestRate:     200.0,
		}
		factory := NewWorkloadAwarePolicyFactoryWithConfig(registry, config)
		policy := factory.CreatePolicy()
		
		if policy == nil {
			t.Fatal("Factory should create non-nil policy")
		}
		
		// Verify custom config is used
		waPolicy := policy.(*WorkloadAwarePolicy)
		if waPolicy.config.WaitTimeWeight != 0.5 {
			t.Errorf("Expected WaitTimeWeight 0.5, got %f", waPolicy.config.WaitTimeWeight)
		}
	})
}

func TestWorkloadAwarePolicy_NilRegistry(t *testing.T) {
	// Policy should handle nil registry gracefully
	policy := NewWorkloadAwarePolicyWithDefaults(nil)
	
	now := time.Now()
	itemA := createMockItem("workload-a", 3, now)
	itemB := createMockItem("workload-b", 4, now)
	
	// Should not panic and should still compare based on criticality
	result := policy.Less(itemB, itemA)
	if !result {
		t.Error("Higher criticality should win even with nil registry")
	}
}

func TestDefaultWorkloadAwarePolicyConfig(t *testing.T) {
	config := DefaultWorkloadAwarePolicyConfig()
	
	if config.WaitTimeWeight != 0.4 {
		t.Errorf("Expected WaitTimeWeight 0.4, got %f", config.WaitTimeWeight)
	}
	if config.CriticalityWeight != 0.4 {
		t.Errorf("Expected CriticalityWeight 0.4, got %f", config.CriticalityWeight)
	}
	if config.RequestRateWeight != 0.2 {
		t.Errorf("Expected RequestRateWeight 0.2, got %f", config.RequestRateWeight)
	}
	if config.MaxWaitTimeSeconds != 60.0 {
		t.Errorf("Expected MaxWaitTimeSeconds 60.0, got %f", config.MaxWaitTimeSeconds)
	}
	if config.MaxRequestRate != 100.0 {
		t.Errorf("Expected MaxRequestRate 100.0, got %f", config.MaxRequestRate)
	}
}

// Made with Bob
